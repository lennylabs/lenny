const { GoogleAuth } = require("google-auth-library");
(async () => {
  const auth = new GoogleAuth({ scopes: ["https://www.googleapis.com/auth/cloud-platform"] });
  let client;
  try { client = await auth.getClient(); } catch (e) { console.log("ERR getClient", String(e.message).slice(0, 400)); return; }
  console.log("client:", client.constructor.name);
  try { console.log("projectId:", await auth.getProjectId()); } catch (e) { console.log("projectId ERR:", String(e.message).slice(0, 300)); }
  try { const c = await auth.getCredentials(); console.log("email:", c.client_email); } catch (e) { console.log("creds ERR", e.message); }
  for (const n of [1, 2]) { try { const r = await client.request({ url: process.env.API_URL }); console.log(`call${n} ->`, JSON.stringify(r.data)); } catch (e) { console.log(`call${n} ERR`, String(e.message).slice(0, 400)); } }
})();
