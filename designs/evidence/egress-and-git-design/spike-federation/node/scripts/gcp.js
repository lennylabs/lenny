const { GoogleAuth } = require("google-auth-library");
const { swap, sleep } = require("./common");
const D = "/var/run/netagent/gcp";
(async () => {
  if (!process.env.NOSWAP) swap(D, 1);
  const auth = new GoogleAuth({ scopes: ["https://www.googleapis.com/auth/cloud-platform"] });
  let client;
  try { client = await auth.getClient(); } catch (e) { console.log("ERR getClient", e.message); return; }
  console.log("client:", client.constructor.name);
  try { console.log("projectId:", await auth.getProjectId()); } catch (e) { console.log("projectId ERR:", String(e.message).slice(0, 300)); }
  const call = async n => { try { const r = await client.request({ url: process.env.API_URL }); console.log(`call${n} ->`, JSON.stringify(r.data)); } catch (e) { console.log(`call${n} ERR`, String(e.message).slice(0, 500)); } };
  await call(1);
  if (!process.env.NOSWAP) swap(D, 2);
  await sleep(); await call(2);
})();
