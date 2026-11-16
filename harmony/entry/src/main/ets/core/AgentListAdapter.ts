import type { AgentListGroupProjection, AgentListInput, AgentListRow, CoreSortInput, CoreAgentListGroup,
  CoreAgentListProjection, CoreProjectSort, AgentSortReport, AgentListPreferences } from './AgentListProjection';
import { compareNativeWithCore, matchesNativeAgentListRow } from './AgentListProjection';
import { Wire } from '../models/Wire';

export interface AgentListAdapter {
  project(input: AgentListInput): AgentListGroupProjection[];
}

export function nativeAgentListProjection(input: AgentListInput): AgentListGroupProjection[] {
  return projectNative(input);
}
import { projectNativeAgentList as projectNative } from './AgentListProjection';

/** Production authority: only the existing ArkTS rules determine visible rows. */
export class NativeAgentListAdapter implements AgentListAdapter {
  project(input: AgentListInput): AgentListGroupProjection[] { return nativeAgentListProjection(input); }
}

/** A core input must name an actual iOS grouping; native-only modes are never relabelled. */
export function coreAgentSortInput(input: AgentListInput): CoreSortInput | undefined {
  const grouping = input.grouping === 'flat' ? 'state' : input.grouping === 'repository' ? 'space' : input.grouping;
  if (!['state', 'space', 'machine'].includes(grouping)) return undefined;
  const quick = {all: 'all', needs_you: 'needsYou', unread: 'unread', working: 'working'}[input.quick];
  if (!quick) return undefined;
  const pinsSpaces: string[] = [], pinsSessions: string[] = [];
  const mutesSpaces: string[] = [], mutesSessions: string[] = [], mutesAllowed: string[] = [];
  const preferences = (row: AgentListRow): AgentListPreferences => {
    const current = input.preferencesFor(row);
    current.pinsSpaces.forEach((value: string) => { if (!pinsSpaces.includes(value)) pinsSpaces.push(value); });
    current.pinsSessions.forEach((value: string) => { if (!pinsSessions.includes(value)) pinsSessions.push(value); });
    current.mutesSpaces.forEach((value: string) => { if (!mutesSpaces.includes(value)) mutesSpaces.push(value); });
    current.mutesSessions.forEach((value: string) => { if (!mutesSessions.includes(value)) mutesSessions.push(value); });
    current.mutesAllowed.forEach((value: string) => { if (!mutesAllowed.includes(value)) mutesAllowed.push(value); });
    return current;
  };
  const sessions = input.rows.filter((row: AgentListRow) => !input.reading ||
    input.kept.includes(coreRowId(row)) || matchesNativeAgentListRow(input, row)).map((row: AgentListRow) => {
    preferences(row);
    return row.raw;
  });
  return {sessions, machines: input.machines, pins: {spaces: pinsSpaces, sessions: pinsSessions},
    mutes: {spaces: mutesSpaces, sessions: mutesSessions, allowed: mutesAllowed},
    settings: {...input.coreSettings, grouping, quick, query: input.query, machine_filter: input.machineFilter,
      kept: input.kept}};
}

export function nativeCoreProjection(input: AgentListInput): CoreAgentListProjection | undefined {
  const coreInput = coreAgentSortInput(input);
  if (!coreInput) return undefined;
  const native = nativeAgentListProjection(input);
  const state = (row: AgentListRow): string => Wire.text(row.raw, 'state', Wire.text(row.raw, 'status'));
  const id = (row: AgentListRow): string => (Wire.text(row.raw, 'app_hub_group') ? Wire.text(row.raw, 'app_hub_group') + '|' : '') +
    Wire.text(row.raw, 'machine_id') + '/' + Wire.text(row.raw, 'session_id');
  const counts: Record<string, Object> = {needsYou: 0, unread: 0, working: 0, backgroundWaiting: 0};
  input.rows.filter((row) => state(row) !== 'offline' && !Wire.bool(row.raw, 'archived')).forEach((row) => {
    const key = {needs_you: 'needsYou', unread: 'unread', working: 'working'}[state(row)];
    if (key) counts[key] = counts[key] as number + 1;
    if (Wire.bool(row.raw, 'background_waiting')) counts['backgroundWaiting'] = counts['backgroundWaiting'] as number + 1;
  });
  const unread = native.flatMap((group) => group.rows.filter((row) => state(row) === 'unread').map(id));
  const groups = native.map((group): CoreAgentListGroup => ({id: group.id, title: group.machineName,
    rows: group.rows.map(id), visible_rows: group.rows.map(id), folded: false}));
  return {counts, groups, unread_shown: unread, ...(coreInput.settings['grouping'] === 'space' ? {quiet_title: null} : {})};
}

/** Debug/test only: compare the same core input without changing native output. */
export function shadowAgentList(input: AgentListInput, native: CoreAgentListProjection | undefined,
  project: CoreProjectSort, report: (report: AgentSortReport) => void): boolean {
  const coreInput = coreAgentSortInput(input);
  if (!coreInput || !native) return false;
  return compareNativeWithCore(coreInput, native, project, report);
}
