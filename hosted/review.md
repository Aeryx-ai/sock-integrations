# Sock review materials

Status: draft, all live cases blocked or not run. No fixtures, users, conversations, credentials or subscriptions were created. Public copy is in `sock/plugin.json`; these notes support rehearsal and secure reviewer setup.

## Reviewer access

- Confirm the intended Aeryx publishing organization/project and verified developer identity in the portal.
- Provide a dedicated sample Sock human account already provisioned for the reviewer, with current terms and the required service/subscription access. Do not use Guy's account or production messages.
- Resolve a reviewer sign-in method that works independently of Guy's Apple account, phone, mailbox, MFA approval or private network. The current normal product flow uses Apple sign-in and invite-only onboarding. Do not invent a bypass or change auth in this packaging task. Follow the portal's current secure reviewer-access instructions for the selected method.
- Record the actual login URL, steps, permissions and access details in secure reviewer fields only. Never place passwords, tokens, account recovery data or sign-in links containing credentials in this folder or the ZIP.
- Complete OAuth in the intended host with the test account. Consent creates or reconnects a connector puppet; record its actual identity in private test notes. Do not pre-register an unrelated Codex task puppet.
- Prepare one owner direct and one sample group containing the connected puppet using the Sock app. Use sample participants only. Record their real ids privately; no invented UUID in a case is acceptable.
- Authorize a single `owner.reply` subscription in the intended chat/dot, with instructions to reply only in the event's conversation. Receive a real owner event before running P3 through P5. Avoid overlapping subscriptions that could fan out the same event to multiple replying agents.
- Verify that the same reviewer can sign in and run every case without help. Keep the account and sample data available for re-review. Document cleanup and unsubscribe after the authorized test.

## Case execution record

Use the exact cases embedded in `sock/plugin.json`; do not maintain a second copy of the prompts here. For every run record the package/source revision, host, actual tool calls/arguments, result, observed message id and case outcome in private evidence. Keep tokens and unrelated message content out of evidence.

| Case | Coverage | Current outcome |
| --- | --- | --- |
| P1 | Connected puppet identity | Blocked: endpoint/auth unavailable |
| P2 | Conversation/member discovery | Blocked: endpoint/auth and sample fixtures unavailable |
| P3 | Plain reply to an owner direct | Blocked: no authenticated owner event or fixture |
| P4 | Markdown and Unicode reply | Blocked: no authenticated owner event or fixture |
| P5 | Reply in the correct sample group | Blocked: no authenticated owner event or group fixture |
| N1 | Unsupported message history | Not run in intended host |
| N2 | Unsupported membership changes | Not run in intended host |
| N3 | Unsupported puppet/token creation | Not run in intended host |

Additional receiving checks: verify that the host discovers `owner.reply`, performs signed callback verification and receives a real event with conversationId, messageId, seq and text. Verify that non-owner messages produce no owner event. Re-deliver a sample event only through an authorized test mechanism and verify no duplicate reply. These checks supplement the required five positive and three negative cases; source tests do not prove the production round trip. Record actual tool annotation values from live discovery: all three boolean hints must match behavior. Do not claim the event stream or annotations were verified merely because their source exists.

## Demo script

1. Rehearse on the exact staged version in the intended host after endpoint/OAuth readiness. Open the sample Sock app beside the connected chat/dot. Show the version and connection name without showing secrets. Verify video recording capability before starting; screenshots alone are not a recording.
2. Show OAuth connect and the connected puppet using P1. Keep credential-entry screens, cookies, tokens and account recovery details off screen. Use the prepared reviewer account.
3. Run P2. Show the returned direct/group ids and sample member ids. No real customer data.
4. Authorize the owner.reply subscription in the dot. Send a new owner message in the sample direct. Show its arrival in this same dot. Run P3, then show the one reply in the same direct and its successful tool result.
5. Send another owner message and run P4. Show readable Markdown/Unicode in both the tool result and Sock app.
6. Send an owner message in the sample group and run P5. Show that the reply stays in that group.
7. Run N1, N2 and N3. Show the clear capability explanation and absence of send, membership or credential-creation actions. Do not substitute tool errors for the expected unsupported-capability response.
8. Stop recording. Play the actual file back, check legibility, case coverage and absence of secrets. Trim credential screens if needed without replacing actual interactions with mockups.
9. With separate authorization, host the real recording where reviewers can play it without requesting access. Verify playback, set `extensions.com.openai.review.demo_recording_url` to that actual HTTPS URL and revalidate the package. A script, screenshot or local video alone does not complete this requirement.

## Publisher decisions before portal setup

Confirm countries (or explicitly all available countries), commerce/payment behavior and actual verified Aeryx identity. Confirm hosted-connector privacy/terms coverage with the publisher. Optional dark icons and brand colors can remain omitted. All online uploads, legal attestations, submission and publication remain separate owner-authorized steps.
