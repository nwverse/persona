import { useEffect, useMemo, useRef } from "react";
import { repositoryRequestContext } from "./task-repositories";

/** Keeps repository requests tied to the mounted editor and its current connection. */
export function useRepositoryRequestContext(manager: string, owner: string) {
  const alive = useRef(true);
  const identity = useRef("");
  identity.current = `${manager}:${owner}`;
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  return useMemo(() => repositoryRequestContext(manager, owner, () => alive.current && identity.current === `${manager}:${owner}`), [manager, owner]);
}
