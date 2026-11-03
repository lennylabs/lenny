const { WorkloadIdentityCredential, DefaultAzureCredential } = require("@azure/identity");
const { swap, sleep } = require("./common");
const D = "/var/run/netagent/azure";
(async () => {
  swap(D, 1);
  const o = process.env.DISABLE_ID === "0" ? {} : { disableInstanceDiscovery: true };
  let cred;
  if (process.env.MODE === "default-args") cred = new DefaultAzureCredential({ ...o, workloadIdentityClientId: process.env.X_CLIENT_ID, tenantId: process.env.X_TENANT_ID });
  else if (process.env.MODE === "default") cred = new DefaultAzureCredential(o);
  else if (process.env.MODE === "args") cred = new WorkloadIdentityCredential({ ...o, tenantId: process.env.X_TENANT_ID, clientId: process.env.X_CLIENT_ID, tokenFilePath: D + "/token" });
  else cred = new WorkloadIdentityCredential(o);
  const call = async n => {
    try {
      const t = await cred.getToken("https://management.azure.com/.default");
      const r = await fetch(process.env.API_URL, { headers: { Authorization: "Bearer " + t.token } });
      console.log(`call${n} ->`, await r.text());
    } catch (e) { console.log(`call${n} ERR`, e.name, String(e.message).slice(0, 700)); }
  };
  await call(1); swap(D, 2); await sleep(); await call(2);
})();
