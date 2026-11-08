# 5. The runners take the owner's puppets

Status: accepted (2026-11-07). Design: [integrations spec](../specs/integrations.md), rule 6 and the runner and forwarder gates.

## Context

Every headless runner (`sock-runner`, `sock-bridge`, `sock-http`) gated on one sender: the puppet's owner, from `account.puppet.ownerId`. Rule 6 said nothing widens that gate.

The owner now has agents that talk to each other. One agent (a desk avatar, a puppet of the owner) relays the owner's spoken questions to another (a coding agent behind `sock-http`) in a group conversation the owner is also in. Its messages arrived from the avatar's puppet id, not the owner's, and were acknowledged and dropped.

## Decision

- A sender passes the gate when it is the owner, or, when the runner is started with `SOCK_ACCEPT_PUPPETS=1`, when `sock accounts get <sender>` shows a puppet whose `ownerId` is the owner. The account is cached a minute per sender, as the owner is per token. A lookup that cannot run leaves the message pending.
- The setting is off by default. Two runners of one owner that both took puppets in one conversation would answer each other without end, bounded only by the hub's 60 sends a minute per puppet. The owner turns it on for the runner that is spoken to.
- Only the owner answers a `sock-bridge` tool confirmation. A puppet's "approve <code>" reaches the session as a message, nothing more.
- The prompt says who sent it. The owner's tag and preface are unchanged; a puppet's tag is `<sock-message from="puppet" name="<display name>" ...>` and its preface names the puppet and says it speaks on the owner's behalf. The name is reduced to a safe character set before it reaches the tag.
- Nothing else changes. The runners know no agent and no avatar: which puppets exist, and what one says to another, is the owner's business and the agents'.

## Alternatives

- A configured list of allowed sender ids: one more setting to keep in step as puppets come and go, for a fact the hub already holds.
- Having the avatar send as the owner: a puppet token cannot, and it would hide who asked.
- A bridge of its own between the two agents: a second transport beside the hub that carries every other message.

## Consequences

- An owner's agents can be addressed by the owner's other agents. A stranger in a shared conversation is still dropped, now after one lookup a minute instead of none.
- A compromised puppet token of the owner's can drive every runner the owner has that takes puppets. Rule 1 already bounds that to one binding per token; the owner erases the puppet. Archiving is not enough: an archived puppet keeps its `ownerId` and keeps passing.
- The agent sees `from="puppet"` and the name, so a model can weigh a relayed question differently from the owner's own words if its instructions say to.
