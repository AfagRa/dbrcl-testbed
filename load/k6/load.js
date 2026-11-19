// k6 load for dbrcl-testbed. One script, three scenarios selected by SCENARIO:
//   steady   constant arrival rate RPS
//   diurnal  sinusoid between MIN_RPS and MAX_RPS with period PERIOD_S (starts at MIN_RPS)
//   spike    constant BASE_RPS plus a legitimate surge to FACTOR x BASE_RPS
//            for SPIKE_DURATION_S starting SPIKE_OFFSET_S after load start
// Every request picks a uid from a pool of UID_POOL users (user-00000 ...) and
// sends it as X-User-ID. All parameters come from env (scripts/load.sh fills
// them from config/experiment.yaml). The end-of-run summary is written to
// SUMMARY_PATH (JSON) when set.
import http from 'k6/http';
import { check } from 'k6';

const env = (k, d) => (__ENV[k] !== undefined && __ENV[k] !== '' ? __ENV[k] : d);
const num = (k, d) => Number(env(k, d));

const SCENARIO = env('SCENARIO', 'steady');
const TARGET = env('TARGET', 'http://router.dbrcl.svc.cluster.local:8080');
const DURATION = num('DURATION_S', 1200);
const POOL = num('UID_POOL', 10000);
const SEED = num('SEED', 42);

function arrival(rate, durationS, startS) {
  const vus = Math.max(10, Math.ceil(rate * 0.5)); // ~0.5 s of in-flight work per arrival
  return {
    executor: 'constant-arrival-rate', exec: 'checkout',
    rate: Math.round(rate), timeUnit: '1s',
    duration: `${durationS}s`, startTime: `${startS}s`,
    preAllocatedVUs: vus, maxVUs: vus * 4,
  };
}

// Piecewise-linear sinusoid, 10 s segments: rate(t) = mid - amp*cos(2*pi*t/P).
function diurnal(minR, maxR, periodS, durationS) {
  const mid = (minR + maxR) / 2, amp = (maxR - minR) / 2, seg = 10;
  const stages = [];
  for (let t = seg; t <= durationS; t += seg) {
    stages.push({ target: Math.round(mid - amp * Math.cos((2 * Math.PI * t) / periodS)), duration: `${seg}s` });
  }
  const vus = Math.max(10, Math.ceil(maxR * 0.5));
  return {
    executor: 'ramping-arrival-rate', exec: 'checkout',
    startRate: Math.round(minR), timeUnit: '1s', stages,
    preAllocatedVUs: vus, maxVUs: vus * 4,
  };
}

function scenarios() {
  switch (SCENARIO) {
    case 'steady':
      return { steady: arrival(num('RPS', 50), DURATION, 0) };
    case 'diurnal':
      return { diurnal: diurnal(num('MIN_RPS', 25), num('MAX_RPS', 75), num('PERIOD_S', 600), DURATION) };
    case 'spike': {
      const base = num('BASE_RPS', 50), factor = num('FACTOR', 4);
      const off = num('SPIKE_OFFSET_S', 300), dur = num('SPIKE_DURATION_S', 180);
      if (off + dur > DURATION) throw new Error('spike window ends after DURATION_S');
      return {
        base: arrival(base, DURATION, 0),
        surge: arrival(base * (factor - 1), dur, off), // base + surge = factor x base
      };
    }
    default:
      throw new Error(`unknown SCENARIO ${SCENARIO}`);
  }
}

export const options = {
  scenarios: scenarios(),
  discardResponseBodies: true,
  summaryTrendStats: ['avg', 'med', 'p(95)', 'p(99)', 'max'],
  tags: { scenario_name: SCENARIO },
};

// mulberry32, seeded per VU: reproducible uid streams for a given SEED.
let state = 0;
function rand() {
  if (state === 0) state = (SEED * 1000003 + __VU * 7919) >>> 0 || 1;
  state = (state + 0x6d2b79f5) >>> 0;
  let t = state;
  t = Math.imul(t ^ (t >>> 15), t | 1);
  t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
}

// setup() runs immediately before the scenarios start; its return value reaches
// handleSummary, so the summary carries the exact load start (UTC ms).
export function setup() {
  return { start_ms: Date.now() };
}

const SKUS = ['book', 'pen', 'mug', 'lamp', 'desk', 'cable'];

export function checkout() {
  const uid = `user-${String(Math.floor(rand() * POOL)).padStart(5, '0')}`;
  const items = [];
  const n = 1 + Math.floor(rand() * 3);
  for (let i = 0; i < n; i++) {
    items.push({ sku: SKUS[Math.floor(rand() * SKUS.length)], qty: 1 + Math.floor(rand() * 3), price_cents: 100 + Math.floor(rand() * 5000) });
  }
  const res = http.post(`${TARGET}/checkout`, JSON.stringify({ items }), {
    headers: { 'Content-Type': 'application/json', 'X-User-ID': uid },
    timeout: '10s',
  });
  check(res, { 'status 200': (r) => r.status === 200 });
}

export function handleSummary(data) {
  const startMs = data.setup_data ? data.setup_data.start_ms : null;
  data.dbrcl = { scenario: SCENARIO, target: TARGET, duration_s: DURATION, uid_pool: POOL, seed: SEED,
    start: startMs ? new Date(startMs).toISOString() : null,
    params: Object.fromEntries(Object.entries(__ENV).filter(([k]) =>
      ['RPS', 'MIN_RPS', 'MAX_RPS', 'PERIOD_S', 'BASE_RPS', 'FACTOR', 'SPIKE_OFFSET_S', 'SPIKE_DURATION_S'].includes(k))) };
  const out = { stdout: textLine(data) };
  const path = env('SUMMARY_PATH', '');
  if (path) out[path] = JSON.stringify(data, null, 2);
  return out;
}

function textLine(d) {
  const m = d.metrics;
  const v = (name, key) => (m[name] && m[name].values[key] !== undefined ? m[name].values[key] : 0);
  return `k6 ${SCENARIO}: requests=${v('http_reqs', 'count')} rate=${v('http_reqs', 'rate').toFixed(1)}/s ` +
    `failed=${(100 * v('http_req_failed', 'rate')).toFixed(3)}% p99=${v('http_req_duration', 'p(99)').toFixed(1)}ms ` +
    `dropped_iterations=${v('dropped_iterations', 'count')}\n`;
}
