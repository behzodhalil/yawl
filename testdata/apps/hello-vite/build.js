const fs = require("fs");
fs.mkdirSync("dist/assets", { recursive: true });
fs.writeFileSync("dist/index.html", '<!doctype html><title>hello-vite</title><div id="root">hello-vite index</div><script src="/assets/app.js"></script>\n');
fs.writeFileSync("dist/assets/app.js", 'console.log("hello-vite app");\n');
