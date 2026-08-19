// An HTTP entry point, not an API other code imports — which is why the synced
// docs check scopes itself to _shared/ rather than to every function.
Deno.serve(() => new Response("ok", { status: 200 }));
