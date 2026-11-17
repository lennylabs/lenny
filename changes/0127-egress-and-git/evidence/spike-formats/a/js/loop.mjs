import { S3Client, ListBucketsCommand } from "@aws-sdk/client-s3";
import fs from "node:fs";
const pkg = JSON.parse(fs.readFileSync("/app/node_modules/@aws-sdk/client-s3/package.json"));
console.log("client-s3", pkg.version, "node", process.version);
const s3 = new S3Client({ region: "us-east-1", forcePathStyle: true });
const dur = +(process.env.LOOP_SECONDS || 0), iv = +(process.env.INTERVAL || 15);
const start = Date.now();
while (true) {
  const t = new Date().toISOString().slice(11, 19);
  try { const r = await s3.send(new ListBucketsCommand({})); console.log(t, "OK", r.Buckets[0].Name); }
  catch (e) { console.log(t, "ERR", e.name, String(e.message).slice(0, 300)); }
  if ((Date.now() - start) / 1000 + iv > dur) break;
  await new Promise(r => setTimeout(r, iv * 1000));
}
