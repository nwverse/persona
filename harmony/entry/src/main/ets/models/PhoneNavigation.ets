// In the compact Separate layout each primary place owns a stack. A pushed destination has one
// Back button, including a session opened directly from a repository row.
const PLACE_ROOTS: string[] = ['', '', 'resources', 'settings_root', '', 'resources', 'resources', 'resources',
  'settings', 'settings', 'recordings', 'applications'];

export function phonePlaceAtRoot(section: number, routes: string[], assistantConversation: boolean): boolean {
  if (!routes.length) return true;
  if (routes.length !== 1) return false;
  if (section === 0) return assistantConversation && routes[0] === 'conversation';
  return !!PLACE_ROOTS[section] && routes[0] === PLACE_ROOTS[section];
}

// Keep drags starting in the composer, vertical lists and nested scrollers with their owners.
// The exposed side of a closed peer also works when the header's buttons are off screen.
export function phonePanIntent(section: number, atRoot: boolean, drawerOpen: boolean, x: number,
  y: number, width: number, height: number, dx: number, dy: number): string {
  if (width <= 0 || height <= 0 || Math.abs(dx) < 12 || Math.abs(dx) < Math.abs(dy) * 1.6) return '';
  if (drawerOpen) return dx < 0 ? 'close' : '';
  if (!atRoot || y > height - 88) return '';
  const header = y <= 54, edge = x <= Math.min(48, width * 0.14);
  if (dx > 0 && edge) return 'open';
  if (section === 0 && dx < 0 && (header || x >= width * 0.60)) return 'agents';
  if (section === 1 && dx > 0 && (header || x <= width * 0.40)) return 'persona';
  return '';
}

export function finishesPhonePan(dx: number, vx: number, width: number): boolean {
  return Math.abs(dx) >= Math.min(94, width * 0.21) || Math.abs(dx) >= 22 && Math.abs(vx) >= 580 && dx * vx > 0;
}
