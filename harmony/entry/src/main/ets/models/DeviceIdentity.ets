import { Wire } from './Wire';

// Device credentials carry an enrollment name. Only parse the fields produced by the
// web sign-in page (or an exact product name); user-assigned names remain intact.
export interface BrowserIdentity {
  family: string;
  system: string;
  title: string;
}

const browserNames: Map<string, string> = new Map<string, string>([
  ['chrome', 'Chrome'],
  ['edge', 'Edge'],
  ['firefox', 'Firefox'],
  ['safari', 'Safari']
]);

const platformNames: Map<string, string> = new Map<string, string>([
  ['macos', 'macOS'],
  ['windows', 'Windows'],
  ['linux', 'Linux'],
  ['android', 'Android'],
  ['ios', 'iOS'],
  ['ipados', 'iPadOS'],
  ['harmonyos', 'HarmonyOS'],
  ['ubuntu', 'Ubuntu'],
  ['debian', 'Debian'],
  ['fedora', 'Fedora'],
  ['redhat', 'Red Hat'],
  ['centos', 'CentOS'],
  ['rocky', 'Rocky Linux'],
  ['alma', 'AlmaLinux'],
  ['arch', 'Arch Linux'],
  ['alpine', 'Alpine Linux'],
  ['opensuse', 'openSUSE'],
  ['nixos', 'NixOS']
]);

export function platformName(family: string): string {
  return platformNames.get(family) || '';
}

function osFamily(value: string): string {
  const os = value.toLowerCase().trim();
  if (/^(darwin|macos|mac os x|os x|mac)(?:$|\s+\d)/.test(os)) return 'macos';
  if (/^(windows(?:_nt)?|win32|win64)(?:$|\s+\d)/.test(os)) return 'windows';
  if (os === 'iphone') return 'ios';
  if (os === 'ipad') return 'ipados';
  return platformName(os) ? os : '';
}

const distroAliases: Map<string, string> = new Map<string, string>([
  ['pop', 'ubuntu'],
  ['linuxmint', 'ubuntu'],
  ['elementary', 'ubuntu'],
  ['zorin', 'ubuntu'],
  ['raspbian', 'debian'],
  ['kali', 'debian'],
  ['rhel', 'redhat'],
  ['almalinux', 'alma'],
  ['manjaro', 'arch'],
  ['endeavouros', 'arch'],
  ['opensuse-leap', 'opensuse'],
  ['opensuse-tumbleweed', 'opensuse'],
  ['suse', 'opensuse'],
  ['sles', 'opensuse']
]);

export function machineSystemFamily(os: string, distro: string, like: string): string {
  const base = osFamily(os);
  if (base && base !== 'linux') return base;
  for (const id of [distro.toLowerCase().trim()].concat(like.toLowerCase().split(/\s+/))) {
    const alias = distroAliases.get(id);
    if (alias) return alias;
    if (platformName(id) && id !== 'linux') return id;
  }
  return base;
}

export function browserIdentity(name: string): BrowserIdentity {
  const fallback: BrowserIdentity = { family: '', system: '', title: name };
  const wrapper = /^(?:Web|网页|ウェブ)\s*[(（]([^()（）]+)[)）]$/i.exec(name.trim());
  const fields = (wrapper ? wrapper[1] : name).split('·').map((s: string) => s.trim());
  let family = fields[0].toLowerCase();
  if (family === 'google chrome') family = 'chrome';
  if (family === 'microsoft edge') family = 'edge';
  if (family === 'mozilla firefox') family = 'firefox';
  const system = osFamily(fields[1] || '');
  if (!wrapper && (fields.length > 2 || (fields.length === 2 && !system))) return fallback;
  const browser = browserNames.get(family);
  if (!browser) {
    if (wrapper) fallback.system = system;
    return fallback;
  }
  const title = [browser].concat(fields.slice(system ? 2 : 1)).filter((v: string) => !!v).join(' · ');
  return { family: family, system: system, title: title };
}


export interface MachinePlatform {
  family: string;
  detail: string;
}

export function machinePlatform(row: Record<string, Object>): MachinePlatform {
  const system = Wire.object(row, 'system');
  const os = Wire.text(system, 'os') || Wire.text(row, 'os') || Wire.text(row, 'platform');
  const family = machineSystemFamily(os, Wire.text(system, 'distro'), Wire.text(system, 'like'));
  let name = Wire.text(system, 'name') || platformName(family) || os;
  const version = Wire.text(system, 'version');
  if (version && !name.includes(version)) name = [name, version].filter((v: string) => !!v).join(' ');
  const detail = [name, Wire.text(system, 'arch') || Wire.text(row, 'arch'), Wire.text(row, 'engine'), Wire.text(row, 'version')];
  return { family: family, detail: detail.filter((v: string) => !!v).join(' · ') };
}
