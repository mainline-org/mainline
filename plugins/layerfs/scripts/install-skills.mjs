import { cp, lstat, mkdir } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

const target = process.argv[2];
if (!target) throw new Error("Usage: npm run install-skills -- /absolute/path/to/agent/skills");
const base = resolve(target);
const sources = [
  ["mainline", new URL("../../../skills/mainline/", import.meta.url)],
  ["mainline-layerfs", new URL("../skills/mainline-layerfs/", import.meta.url)],
  ["layerfs-js-sdk", new URL("../skills/layerfs-js-sdk/", import.meta.url)],
];
// Check every destination before writing. Updating existing skills is an explicit action.
for (const [name] of sources) {
  try { await lstat(resolve(base, name)); }
  catch (error) { if (error.code === "ENOENT") continue; throw error; }
  throw new Error(`${resolve(base, name)} already exists; select a fresh directory or update it explicitly`);
}
await mkdir(base, { recursive: true });
for (const [name, source] of sources) await cp(fileURLToPath(source), resolve(base, name), { recursive: true, force: false, errorOnExist: true });
console.log(`Installed Mainline, Mainline for LayerFS and the pinned LayerFS SDK skill into ${base}`);
