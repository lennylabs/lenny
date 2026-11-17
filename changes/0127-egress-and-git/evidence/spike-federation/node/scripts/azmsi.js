const { ManagedIdentityCredential, DefaultAzureCredential } = require("@azure/identity");
(async () => {
  const cred = process.env.MODE === "default" ? new DefaultAzureCredential() : (process.env.X_CLIENT_ID ? new ManagedIdentityCredential({ clientId: process.env.X_CLIENT_ID }) : new ManagedIdentityCredential());
  try { const t = await cred.getToken("https://management.azure.com/.default"); console.log("token:", t.token, "expiresOn:", t.expiresOnTimestamp); }
  catch (e) { console.log("ERR", e.name, String(e.message).slice(0, 700)); }
})();
