// Go-style duration strings ("90s", "1h30m"), which k6's executors and the
// DURATION knob are written in, as seconds and back.

// parseDuration converts a Go duration string such as "12m" or "1h30m5s"
// into seconds.
export function parseDuration(text) {
  if (text === undefined || text === null || text === "" || text === "0") return 0;
  const units = { ns: 1e-9, us: 1e-6, µs: 1e-6, ms: 1e-3, s: 1, m: 60, h: 3600 };
  const pattern = /(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)/g;
  let seconds = 0;
  let consumed = 0;
  for (const match of text.matchAll(pattern)) {
    seconds += Number(match[1]) * units[match[2]];
    consumed += match[0].length;
  }
  if (consumed !== text.length) throw new Error(`${text} is not a Go duration`);

  return seconds;
}

// formatDuration writes seconds as a Go duration string, whole seconds only.
export function formatDuration(seconds) {
  const s = Math.max(0, Math.floor(seconds));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const rest = s % 60;

  return `${h ? `${h}h` : ""}${m ? `${m}m` : ""}${rest || (!h && !m) ? `${rest}s` : ""}`;
}
