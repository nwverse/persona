import native from 'libpersona_core_ohos.so';
import type { AgentListInput, CoreAgentListProjection, CoreSortInput } from './AgentListProjection';
import { Wire } from '../models/Wire';

export interface AgentListAdapter {
  project(input: AgentListInput): CoreAgentListProjection;
}

/** The production adapter has one sorter: the shared Rust core. */
export class CoreAgentListAdapter implements AgentListAdapter {
  project(input: AgentListInput): CoreAgentListProjection {
    const projected = Wire.record(JSON.parse(native.coreProjectSort(JSON.stringify(coreAgentSortInput(input)))) as Object) as Object as CoreAgentListProjection;
    if (!Array.isArray(projected.groups) || !Array.isArray(projected.unread_shown) || !projected.counts) throw new Error('invalid_core_projection');
    return projected;
  }
}

export function coreAgentSortInput(input: AgentListInput): CoreSortInput {
  const machineFilter = coreMachineFilter(input);
  if (machineFilter === undefined) throw new Error('unsupported_machine_filter');
  const grouping = input.grouping === 'flat' ? 'state' : input.grouping === 'repository' ? 'space' : input.grouping;
  if (!['state', 'space', 'machine'].includes(grouping)) throw new Error('unsupported_grouping');
  const quick = {all: 'all', needs_you: 'needsYou', unread: 'unread', working: 'working'}[input.quick] || input.quick;
  if (!['all', 'needsYou', 'unread', 'working', 'backgroundWaiting'].includes(quick)) throw new Error('unsupported_quick_filter');
  const pinsSpaces: string[] = [], pinsSessions: string[] = [];
  const mutesSpaces: string[] = [], mutesSessions: string[] = [], mutesAllowed: string[] = [];
  input.rows.forEach((row) => {
    const current = input.preferencesFor(row);
    current.pinsSpaces.forEach((value) => { if (!pinsSpaces.includes(value)) pinsSpaces.push(value); });
    current.pinsSessions.forEach((value) => { if (!pinsSessions.includes(value)) pinsSessions.push(value); });
    current.mutesSpaces.forEach((value) => { if (!mutesSpaces.includes(value)) mutesSpaces.push(value); });
    current.mutesSessions.forEach((value) => { if (!mutesSessions.includes(value)) mutesSessions.push(value); });
    current.mutesAllowed.forEach((value) => { if (!mutesAllowed.includes(value)) mutesAllowed.push(value); });
  });
  return {sessions: input.rows.map((row) => row.raw), machines: input.machines,
    pins: {spaces: pinsSpaces, sessions: pinsSessions}, mutes: {spaces: mutesSpaces, sessions: mutesSessions, allowed: mutesAllowed},
    settings: {...input.coreSettings, grouping, quick, query: input.query, machine_filter: machineFilter, kept: input.kept}};
}

export function coreMachineFilter(input: AgentListInput): string | undefined {
  if (!input.machineFilter) return '';
  if (input.machineFilter.includes('|')) return input.machineFilter;
  if (input.machineFilter[0] === '[') {
    try {
      const parsed = JSON.parse(input.machineFilter) as Object[];
      if (parsed.length !== 3) return undefined;
      const group = typeof parsed[0] === 'string' ? parsed[0] as string : '';
      const id = typeof parsed[2] === 'string' ? parsed[2] as string : '';
      return id && group ? group + '|' + id : id || undefined;
    } catch (_) { return undefined; }
  }
  return input.machineFilter;
}

export function nativeCoreProjection(input: AgentListInput): CoreAgentListProjection {
  return JSON.parse(native.coreProjectSort(JSON.stringify(coreAgentSortInput(input)))) as CoreAgentListProjection;
}
