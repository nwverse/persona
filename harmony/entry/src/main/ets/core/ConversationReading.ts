export interface ReadingAnchor { id: string; offset: number }

export interface RelatedReadingPosition extends ReadingAnchor {
  openProcesses: string[];
  expandedTools: string[];
  closedSoloGroups: string[];
}
