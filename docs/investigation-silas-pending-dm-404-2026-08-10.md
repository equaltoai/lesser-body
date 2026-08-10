# Investigation: Silas pending direct message returned 404 (2026-08-10)

## Reported symptom

Silas called `direct_messages_read` for Della and received `404 conversation not found`, even though Della had sent him
a direct message.

## Verified evidence

- TheoryLive Lesser accepted Della's direct-message create request at 2026-08-10 15:07:00 UTC with HTTP 201.
- Lesser assigned message id `1c2ddc2b-b345-4d12-b2ef-22fa1947dc6b` and conversation id
  `efc75d63-4766-4dbf-8a08-d145bb4c3397`.
- The conversation metadata names `della-marlowe` and `silas-vane` as participants and records one message.
- Della's viewer state is `ACCEPTED` in `INBOX`.
- Silas's recipient state is `PENDING` in `REQUESTS`, with one unread item and `della-marlowe` as the counterpart.
- Lesser's named `/api/v1/conversations/lookup` route searches the accepted inbox surface, so it can return 404 while a
  first-contact conversation exists in the recipient's request folder.

The message exists. This is not message loss and is distinct from the pre-dispatch concurrency failure addressed by
PR #552.

## Fix-locus verdict

Fix the recovery behavior in Body without weakening Lesser's request gate. `direct_messages_read` already owns the
named-counterpart MCP workflow and Body already consumes Lesser's recipient-authorized request-folder GraphQL query.
On an inbox lookup miss, Body can make one bounded request-folder read, identify a matching pending counterpart, and
return the existing preview and decision actions. No Lesser contract or data migration is required.

## Authorization and tool-surface audit

- `direct_messages_read` remains read-scoped and available in both drone and souled profiles.
- The fallback forwards the same actor OAuth bearer to Lesser and reads only the authenticated recipient's `REQUESTS`
  folder.
- The read tool never accepts or declines a request. It returns the existing write-scoped
  `message_request_accept` / `message_request_decline` actions for an explicit subsequent decision.
- Existing success output and input schemas remain unchanged. A matching pending request changes a misleading
  `not_found` into the more precise `message_request_pending` tool error with status 409 and bounded request metadata.
- A true miss remains `not_found` and now includes a machine-readable `message_requests_list` action.
- The fallback reads at most 80 pending requests, performs no pagination loop, and does not scan notifications,
  timelines, email, or Lesser tables at runtime.

## Lesser-integration and MCP-contract audit

- Uses the existing Lesser REST lookup and existing `BodyMessageRequests` GraphQL operation; no endpoint, query shape,
  JWT, DynamoDB, SSM, or deploy-order change.
- `.well-known/mcp.json`, tool registration, input schema, and success output schema remain stable.
- Compatibility classification: semantic refinement with an additive observable error code for the pending-request
  case. MCP clients that handle tool errors generically remain compatible; clients can follow the returned action.

## Verification

1. With a pending first-contact message, call `direct_messages_read` by username and confirm
   `message_request_pending`, the correct conversation id, bounded preview, and explicit accept/decline actions.
2. Confirm the read call does not change the request state.
3. Call `message_request_accept` using the returned conversation id.
4. Retry `direct_messages_read` and confirm the accepted conversation and messages are returned.
5. Call with a truly absent counterpart and confirm `not_found` plus `checkPendingRequests` guidance.
