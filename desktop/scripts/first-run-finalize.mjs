import { firstRunVerdict } from "./first-run-verdict.mjs";

/** One shared parent aggregation: cleanup's final observations and DOM steps must agree with the report and exit. */
export function finalizeFirstRun({ platform, signature, failure, blocked, steps, observations }) {
  const allSteps = steps.map(step => ({ ...step }));
  let finalFailure = failure;
  for (const observation of observations) {
    if (observation.ok) continue;
    allSteps.push({ name: `${observation.phase} final diagnostics`, ok: false, detail: observation.unexpected });
    finalFailure ??= observation.failure ?? observation.unexpected?.[0]?.reason ?? `${observation.phase} has unexpected diagnostics`;
  }
  if (allSteps.some(step => !step.ok)) finalFailure ??= "one or more first-run assertions failed";
  const verdict = firstRunVerdict({ platform, signature, failure: finalFailure, blocked, passed: allSteps.length });
  return {
    verdict,
    steps: allSteps,
    failure: finalFailure,
    exitCode: verdict.ok && allSteps.length > 0 && allSteps.every(step => step.ok) && observations.every(item => item.ok) ? 0 : 1,
  };
}
