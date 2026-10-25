import { execFileSync } from "node:child_process";
import { readFileSync, realpathSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const repository = fileURLToPath(new URL("../../../", import.meta.url));
export const checkout = resolve(repository, "../layerfs");
export const pin = JSON.parse(readFileSync(resolve(repository, "layerfs.json"), "utf8")).commit;

export function verifyCheckout() {
  if (!/^[0-9a-f]{40}$/.test(pin)) throw new Error("layerfs.json must contain a 40-character commit SHA");
  const git = (...args) => execFileSync("git", ["-C", checkout, ...args], { encoding: "utf8" }).trim();
  if (realpathSync(git("rev-parse", "--show-toplevel")) !== realpathSync(checkout)) throw new Error("../layerfs must be its own checkout");
  const actual = git("rev-parse", "HEAD");
  if (actual !== pin) throw new Error(`LayerFS HEAD is ${actual}; required ${pin}`);
  if (git("status", "--porcelain")) throw new Error("LayerFS checkout has changes; it must remain unchanged");
  return pin;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  console.log(`LayerFS verified: ${verifyCheckout()}`);
}
