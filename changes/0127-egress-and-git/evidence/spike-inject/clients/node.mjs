const r = await fetch("https://api.example.test/acme/check", {headers:{Authorization:"Bearer "+process.env.ACME_API_KEY}});
console.log("node-fetch", r.status, (await r.text()).trim());
