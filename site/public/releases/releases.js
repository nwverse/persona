// The public Releases page's DOM-free presentation rules, tested without a browser.
// All URLs come from the Worker's validated metadata and are checked again before entering the DOM.
export const RELEASES_PAGE = "https://github.com/nwverse/persona/releases";

const DOWNLOAD = "https://github.com/nwverse/persona/releases/download/";
const DESKTOP = /^Persona-(.+)-(darwin|linux|windows)-(amd64|arm64)\.(tar\.gz|zip)$/;

export function directDownload(asset) {
  return typeof asset?.url === "string" && asset.url.startsWith(DOWNLOAD) ? asset.url : "";
}

export function fileSize(bytes) {
  if (!Number.isSafeInteger(bytes) || bytes < 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB"];
  let value = bytes / 1024, unit = 0;
  while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit++; }
  return `${value >= 100 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

export function assetLabel(name) {
  if (name.endsWith(".apk")) return "Android";
  const match = DESKTOP.exec(name);
  if (match) {
    const os = { darwin: "macOS", linux: "Linux", windows: "Windows" }[match[2]];
    const arch = { arm64: "ARM64", amd64: "Intel / x64" }[match[3]];
    return `${os} · ${arch}`;
  }
  if (name === "SHA256SUMS") return "SHA256 校验文件";
  if (name === "install.sh") return "安装脚本";
  return name;
}

export function assetDetail(name) {
  if (name.endsWith(".apk")) return "Android 安装包";
  const match = DESKTOP.exec(name);
  if (match) return match[2] === "darwin" && match[3] === "arm64" ? "Apple 芯片" : match[4] === "zip" ? "ZIP 压缩包" : "tar.gz 压缩包";
  return "版本发布产物";
}

export function publishedDate(iso) {
  if (typeof iso !== "string" || !Number.isFinite(Date.parse(iso))) return "";
  return new Intl.DateTimeFormat("zh-CN", { year: "numeric", month: "long", day: "numeric", timeZone: "UTC" }).format(new Date(iso));
}

export function fetchedTime(iso) {
  if (typeof iso !== "string" || !Number.isFinite(Date.parse(iso))) return "";
  return new Intl.DateTimeFormat("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(new Date(iso));
}

export function latestRelease(data) {
  return data?.releases?.find((release) => release.tag === data.latest && !release.prerelease) || null;
}

export function partitionAssets(release) {
  const groups = { android: [], desktop: [], other: [], checksums: [] };
  const labels = new Set();
  for (const asset of release?.assets || []) {
    if (!directDownload(asset) || !groups[asset.kind]) continue;
    // A malformed upstream duplicate must not create two indistinguishable download actions.
    const label = assetLabel(asset.name);
    if (asset.kind !== "other" && labels.has(label)) continue;
    labels.add(label);
    groups[asset.kind].push(asset);
  }
  for (const kind of ["android", "desktop"]) groups[kind].sort((a, b) => assetLabel(a.name).localeCompare(assetLabel(b.name), "zh-CN"));
  groups.other.sort((a, b) => a.name.localeCompare(b.name));
  return groups;
}
