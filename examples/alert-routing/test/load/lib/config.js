// The suite's knobs, read once from the environment. run.sh passes each one
// into the k6 container, so they work the same from `mise run loadtest`:
//
//   TEST_MODE           smoke, load (default), stress, spike, soak or breakpoint
//   RATE                requests per second the routing scenarios start at (default 100);
//                       stress ramps to 5x, spike jumps to 10x, breakpoint climbs to BREAKPOINT_MAX_RATE
//   DURATION            how long load and soak hold RATE, and how long breakpoint
//                       takes to climb (defaults: 2m, 1h and 10m)
//   BREAKPOINT_MAX_RATE the rate breakpoint ends at if nothing gives first (default 50x RATE)
//   WEBHOOK_SHARE       fraction of requests that are webhook batches (default 0.3);
//                       the rest route one alert each
//   MAX_BATCH           largest webhook batch, in alerts (default 100)
//   RELOADS_PER_MINUTE  policy reloads the reload scenario starts per minute (default 6)
//   RELOAD_CONCURRENCY  reloads each of those sends at once (default 2)
//   VUS, MAX_VUS        virtual users preallocated and allowed for each routing scenario
//   P95_MS, P99_MS      latency budget of a single-alert route (defaults: 250 and 500)
//   WEBHOOK_P95_MS,     latency budget of a webhook batch, which evaluates up to
//   WEBHOOK_P99_MS      MAX_BATCH alerts (defaults: 1000 and 2000)
//   SEED                seeds the alert generator, so a failing run can be replayed
//                       (run.sh picks one at random and the report records it)
//   RUN_ID              names the run in Mimir (testid) and results/ (default: mode and time)
//   BASE_URL            alertrouter's address (compose sets http://alertrouter:8080)
//   REQUESTS_DIR        where requests/ is mounted (default /requests)

const modes = ['smoke', 'load', 'stress', 'spike', 'soak', 'breakpoint'];

// Only load, soak and breakpoint read DURATION; the other modes have a fixed
// shape, so their length is part of what the mode means.
const defaultDurations = { load: '2m', soak: '1h', breakpoint: '10m' };

export const mode = __ENV.TEST_MODE || 'load';
if (!modes.includes(mode)) {
  throw new Error(`unknown TEST_MODE ${mode}; use ${modes.join(', ')}`);
}

export const baseURL = __ENV.BASE_URL || 'http://alertrouter:8080';
export const requestsDir = __ENV.REQUESTS_DIR || '/requests';

export const rate = positive('RATE', 100);
export const duration = __ENV.DURATION || defaultDurations[mode] || '2m';
export const breakpointMaxRate = positive('BREAKPOINT_MAX_RATE', rate * 50);
export const webhookShare = fraction('WEBHOOK_SHARE', 0.3);
export const maxBatch = positive('MAX_BATCH', 100);
export const reloadsPerMinute = positive('RELOADS_PER_MINUTE', 6);
export const reloadConcurrency = positive('RELOAD_CONCURRENCY', 2);
export const vus = positive('VUS', 20);
export const maxVUs = positive('MAX_VUS', 100);
// Every VU runs this file on its own, so a default taken from the clock would
// differ between them and couldn't be replayed; run.sh picks the random one.
export const seed = positive('SEED', 1);

export const budget = {
  route: { p95: positive('P95_MS', 250), p99: positive('P99_MS', 500) },
  webhook: { p95: positive('WEBHOOK_P95_MS', 1000), p99: positive('WEBHOOK_P99_MS', 2000) },
};

export const runID = __ENV.RUN_ID || `${mode}-${Date.now()}`;
if (!/^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(runID)) {
  throw new Error('RUN_ID must start with a letter or digit and contain only letters, digits, dots, underscores or hyphens');
}

// The run's provenance, which run.sh collects on the host: the container
// can't see the git checkout or the Docker daemon.
export const provenance = {
  git_commit: __ENV.GIT_COMMIT || 'unknown',
  git_changed_files: __ENV.GIT_CHANGED_FILES === undefined ? null : Number(__ENV.GIT_CHANGED_FILES),
  docker: __ENV.DOCKER_INFO ? JSON.parse(__ENV.DOCKER_INFO) : null,
};

// positive reads a positive number from the environment, so a typo fails the
// run at once instead of silently testing at rate NaN.
function positive(name, fallback) {
  if (__ENV[name] === undefined || __ENV[name] === '') return fallback;
  const value = Number(__ENV[name]);
  if (!(value > 0)) throw new Error(`${name} must be a positive number, not ${__ENV[name]}`);
  return value;
}

// fraction reads a number between 0 and 1 from the environment.
function fraction(name, fallback) {
  if (__ENV[name] === undefined || __ENV[name] === '') return fallback;
  const value = Number(__ENV[name]);
  if (!(value >= 0 && value <= 1)) throw new Error(`${name} must be between 0 and 1, not ${__ENV[name]}`);
  return value;
}
