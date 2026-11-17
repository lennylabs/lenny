const fs = require("fs");
exports.swap = (d, v) => { fs.copyFileSync(`${d}/token.v${v}`, `${d}/token.tmp`); fs.renameSync(`${d}/token.tmp`, `${d}/token`); };
exports.sleep = () => new Promise(r => setTimeout(r, 1000 * parseInt(process.env.SLEEP || "31")));
