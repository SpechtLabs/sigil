// Next calls register() once per server process, before it serves a request.
// It is the service's entry point: the Node runtime boots the composition
// root; the edge runtime, which alertrouter never uses, does nothing.

export async function register(): Promise<void> {
  if (process.env.NEXT_RUNTIME !== "nodejs") return;
  const { boot } = await import("./server/boot");
  await boot(process.env);
}
