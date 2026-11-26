import { expect, it } from "vitest";
import { appPath } from "./deep-links";
import { href, parse } from "./router";

it.each(["/machines", "/machines/Computer%20B", "/machines/b/work", "/machines/b/files",
  "/machines/b/storage?mode=reclaim", "/machines/b/diagnostics", "/machines/b/procs", "/machines/b/services",
  "/machines/b/toolchain", "/machines/b/network", "/machines/b/management", "/machines/b/config",
  ...["1h", "6h", "24h"].flatMap(range => [`/machines/b?range=${range}`, `/machines/b/monitoring?range=${range}`]),
])("preserves the machine address %s", path => {
  const [pathname, query] = path.split("?");
  expect(href(parse(pathname!, query))).toBe(path);
  expect(appPath("persona://open" + path)).toBe(path);
});

it.each(["/machines/b/missing", "/machines/b/files/extra"])("rejects an unknown view at %s", path => {
  expect(parse(path).name).toBe("not-found");
  expect(appPath("persona://open" + path)).toBeNull();
});
