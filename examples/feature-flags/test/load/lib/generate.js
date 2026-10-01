// Randomized evaluation contexts. The named cases in requests/cases.json pin
// the outcome of each rule; this covers the combinations nobody wrote down, at
// volume. It doesn't say what a context must evaluate to, because that would
// mean reimplementing the rollout hash in JavaScript: it checks what has to
// hold whatever the policies say (see checks.js), and lets the named cases
// judge the values.

// The mix of what the generator asks for: mostly free users in a ready
// region, some pro and enterprise, a few beta testers, and a few users in a
// region the platform guardrail has not opened, which must never be enabled.
const plans = [
  ["free", 0.7],
  ["pro", 0.2],
  ["enterprise", 0.1],
];
const regions = [
  ["eu-1", 0.35],
  ["us-1", 0.35],
  ["eu-2", 0.1],
  ["us-2", 0.1],
  ["ap-1", 0.05],
  ["sa-1", 0.05],
];
const betaShare = 0.05;
const cohortShare = 0.05;
const partnerShare = 0.05;

// Generator is one VU's source of contexts. It is deterministic for a seed, so
// a run that found a wrong answer can be replayed with the same SEED.
export class Generator {
  constructor(seed, flags) {
    this.random = mulberry32(seed);
    this.flags = flags;
    this.sequence = 0;
  }

  // context returns an OFREP evaluation context. Every key is unique across
  // VUs, so the hash spreads them over all 100 buckets.
  context() {
    const context = {
      targetingKey: `user-${Math.floor(this.random() * 1e9).toString(36)}-${this.sequence++}`,
      plan: pick(this.random, plans),
      region: pick(this.random, regions),
      beta: this.random() < betaShare,
    };
    if (this.random() < cohortShare) context.cohort = "checkout-pilot";
    if (this.random() < partnerShare) context.partner = this.random() < 0.5 ? "acme" : "initech";

    return context;
  }

  // request returns a generated context and its OFREP request body.
  request() {
    const context = this.context();

    return { context, body: JSON.stringify({ context }) };
  }

  // flag returns one of the served flags at random.
  flag() {
    return this.flags[Math.floor(this.random() * this.flags.length)];
  }
}

// pick draws one value from [[value, weight], ...] whose weights add up to 1.
function pick(random, weighted) {
  let roll = random();
  for (const [value, weight] of weighted) {
    roll -= weight;
    if (roll < 0) return value;
  }

  return weighted[weighted.length - 1][0];
}

// mulberry32 is a small seedable generator; k6 has no seedable Math.random.
function mulberry32(seed) {
  let state = seed >>> 0;
  return () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let t = state;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
