const { DynamoDBClient, ListTablesCommand } = require("@aws-sdk/client-dynamodb");
const { swap, sleep } = require("./common");
const D = "/var/run/netagent/aws";
(async () => {
  swap(D, 1);
  const c = new DynamoDBClient({});
  console.log("region:", await c.config.region());
  const call = async n => { try { const o = await c.send(new ListTablesCommand({})); console.log(`call${n} ->`, o.TableNames); } catch (e) { console.log(`call${n} ERR`, e.name, String(e.message).slice(0, 500)); } };
  await call(1); swap(D, 2); await sleep(); await call(2);
})();
