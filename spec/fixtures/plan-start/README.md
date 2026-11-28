# Open-plan start presentation fixtures

These samples pin `PlanStartView` and every `PlanStartReason` from `schema/wire/plan-start.schema.json` (#17853).

`expected.start` is the optional `start` field on an open `TaskPlan`. `remember` contains zero-based indexes of writable tasks not covered by `tasks.trust`; it appears only with `reason: "start_authorization"`. A hard gate has no useful trust rule to offer. The whole field is absent from older hubs and on plans that are not open.
