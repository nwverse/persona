-- Marks the routed items Persona wrote before routed bodies said so (#1542, docs/protocol/protocol.md "routed"): the apps
-- fold what Persona sent to a task's agent instead of drawing it as the user's own bubble, and stored history reads the
-- same way. The feed already tells them apart: only agent_interrupt writes "interrupted"; agent_send with a task writes
-- the task's card and, right after it, what it sent; a supervision rework opens with the fixed line of
-- prompts/assistant/supervise_rework.md. Everything else stays the user's (a task card's Message, a decision relayed from
-- Lark). The one case it can't tell, agent_send with a task to an agent already on that task (no card is written), keeps
-- the bubble.
UPDATE assistant_feed r SET body = r.body || '{"from":"assistant"}'::jsonb
WHERE r.kind = 'routed' AND NOT r.body ? 'from' AND (
    r.body ? 'interrupted'
    OR r.body->>'text' LIKE '（督办：验收没通过，第%'
    OR EXISTS (SELECT 1 FROM assistant_feed t
               WHERE t.kind = 'task' AND t.conversation = r.conversation AND t.ref = r.ref
                 AND t.at <= r.at AND t.at >= r.at - interval '5 seconds'));
