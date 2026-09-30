// Times on the wire as Go's encoding/json writes a UTC time.Time: RFC 3339
// with the fraction's trailing zeros trimmed, "2026-01-01T00:00:00Z" or
// "2026-01-01T00:00:00.12Z".

export function formatTime(d: Date): string {
  const iso = d.toISOString(); // always .sssZ
  return iso.replace(/\.(\d*?)0*Z$/, (_m, frac: string) => (frac === "" ? "Z" : `.${frac}Z`));
}
