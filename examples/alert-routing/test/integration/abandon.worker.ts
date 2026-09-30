// A client that gives up on a request, in a worker of its own. The server
// under test shares the test's thread, so anything that holds that thread
// (the platform engine evaluates there, synchronously) would also hold a
// client's timer on it. Here the timer fires on time and the connection
// closes mid-evaluation, whatever the server's thread is doing.

// The worker scope, typed as the Worker it talks to the test through.
const scope = self as unknown as Worker;

interface Job {
  url: string;
  body: string;
  waitMs: number;
}

scope.onmessage = async (event: MessageEvent<Job>) => {
  const { url, body, waitMs } = event.data;
  try {
    const res = await fetch(url, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body,
      signal: AbortSignal.timeout(waitMs),
    });
    await res.body?.cancel();
    scope.postMessage({ answered: res.status });
  } catch (err) {
    scope.postMessage({ abandoned: (err as { name?: string }).name ?? String(err) });
  }
};
