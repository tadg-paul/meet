# Acceptance Criteria

This is the canonical spec. ACs introduced from 2026-08-04 onward live here.
Pre-cutover ACs remain in their originating issues until cited or migrated.

Last migrated: AC13.4 from #13 on 2026-08-05

---

## Moderator Access

### AC12.1 - Given moderator email-to-room relationships loaded from host secrets, the system limits moderator-link delivery to authorized email and room pairs without disclosing whether an email address or room is configured.

- Introduced: #12 (closed 2026-08-04)
- Tests:
  - ✅ RT-12.1: Allowed email on `/allowed-room/moderator` shows the generic check-email page and records an outbound message in a capture mailer.
  - ✅ RT-12.2: Allowed email on `/other-room/moderator` shows the same generic check-email page and records no outbound message.
  - ✅ RT-12.3: Unknown email on an otherwise valid room shows the same generic check-email page and records no outbound message.
  - ✅ RT-12.4: Invalid email syntax is rejected as malformed input before authorization lookup.
  - ✅ RT-12.5: Empty room names and room names containing slashes have no moderator-link delivery.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC12.2 - Given an authorized email and room, the emailed magic link is scoped to that email-room pair, has a configurable expiry, and keeps secret token material out of ordinary page content and operator logs.

- Introduced: #12 (closed 2026-08-04)
- Tests:
  - ✅ RT-12.6: The generated email contains a verify URL for the requested room and does not contain a moderator JWT.
  - ✅ RT-12.7: The configured magic-link TTL controls the token expiry accepted by the verifier.
  - ✅ RT-12.8: Captured logs for the web and CLI paths mask or omit full email addresses and do not include raw magic-link tokens or moderator JWTs.
  - ✅ RT-12.9: Two different room authorizations for the same email produce tokens that verify only for their own room payload.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC12.3 - Given a valid unexpired magic link, the system grants a short-lived moderator URL whose JWT is bound to the authorized room; expired, tampered, reused, or wrong-room links have no moderator access.

- Introduced: #12 (closed 2026-08-04)
- Tests:
  - ✅ RT-12.10: A valid magic link redirects to the authorized room with a moderator JWT query parameter.
  - ✅ RT-12.11: The resulting moderator JWT permits local room-window bypass for the authorized room.
  - ✅ RT-12.12: The same JWT has no local room-window bypass for a different room.
  - ✅ RT-12.13: An expired magic link displays the invalid-link page and sets no moderator JWT.
  - ✅ RT-12.14: A tampered magic link displays the invalid-link page and sets no moderator JWT.
  - ✅ RT-12.15: A previously consumed magic link displays the invalid-link page and sets no moderator JWT.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC12.4 - Given the existing room registry, guest participation remains governed by the latest room lifecycle row and its valid window, including reused room names.

- Introduced: #12 (closed 2026-08-04)
- Tests:
  - ✅ RT-12.16: A guest can join a reused room name during the latest active window.
  - ✅ RT-12.17: A guest cannot join a reused room name outside the latest active window.
  - ✅ RT-12.18: A cancelled latest row prevents guest access even when an earlier row for the same room was active.
  - ✅ RT-12.19: Moderator magic-link authorization for a room does not make the plain room URL joinable outside the room window.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC12.5 - Given SMTP configuration is absent or delivery fails, the system fails closed for moderator-link delivery while preserving the generic external response and operator-visible diagnostics.

- Introduced: #12 (closed 2026-08-04)
- Tests:
  - ✅ RT-12.20: Missing SMTP configuration shows the generic check-email page and creates no magic-link token usable by the verifier.
  - ✅ RT-12.21: SMTP delivery failure shows the generic check-email page and creates no magic-link token usable by the verifier.
  - ✅ RT-12.22: SMTP delivery failure creates an operator-visible warning without logging raw token material.
  - ✅ RT-12.23: A successful SMTP handoff is required before the verifier accepts the corresponding token.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC12.6 - Given a single-segment room path, the moderator entry page exists at `/<room>/moderator` and its authorization context is the path room.

- Introduced: #12 (closed 2026-08-04)
- Tests:
  - ✅ RT-12.24: `GET /workshop/moderator` renders an email-only moderator entry form scoped to `workshop`.
  - ✅ RT-12.25: Form submission on `/workshop/moderator` evaluates authorization for `workshop` rather than a room supplied by form data.
  - ✅ RT-12.26: `GET /bad/name/moderator` has no moderator entry page.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC12.7 - Given operator CLI use, super-moderator URLs for all rooms remain available independent of moderator-auth definitions.

- Introduced: #12 (closed 2026-08-04)
- Tests:
  - ✅ RT-12.27: `meet token --room workshop` prints a moderator URL when no moderator-auth relationships are configured.
  - ✅ RT-12.28: The CLI-generated super-moderator JWT carries an all-rooms claim.
  - ✅ RT-12.29: The CLI-generated super-moderator JWT permits local room-window bypass for a different room during its token lifetime.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC12.8 - Given operator CLI use and host-secret moderator relationships, authorized email-room pairs have a CLI path for room-scoped moderator-link delivery.

- Introduced: #12 (closed 2026-08-04)
- Tests:
  - ✅ RT-12.30: The CLI sends or prints a room-scoped magic link for an authorized email-room pair using the same config and secrets cascade as the service.
  - ✅ RT-12.31: The CLI rejects an unauthorized email-room pair without creating a verifier-accepted token.
  - ✅ RT-12.32: A CLI-generated room-scoped magic link produces a moderator JWT for the authorized room only.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

---

## Room Access Gate

### AC13.1 - Given any public single-segment meeting-room path that is not active now, the response is HTTP 404 carrying H1 `Inactive` and the configured inactive-room explanation, and that response is identical in status code, response headers, and body bytes across every blocked state (unregistered, before-window, after-window, cancelled), disclosing nothing about whether the slug is registered.

- Introduced: #13 (closed 2026-08-05)
- Tests:
  - ✅ RT-13.1: A guest loading an unregistered top-level slug receives HTTP 404 with H1 `Inactive` and the specified explanatory paragraph.
  - ✅ RT-13.2: A guest loading a registered room before its active window receives HTTP 404 with the same inactive-room page.
  - ✅ RT-13.3: A guest loading a registered room after its active window receives HTTP 404 with the same inactive-room page.
  - ✅ RT-13.4: A guest loading a cancelled latest room row receives HTTP 404 with the same inactive-room page.
  - ✅ RT-13.5: The responses for unregistered, before-window, after-window, and cancelled top-level slugs are byte-for-byte identical in status code, response headers, and body.
  - ✅ RT-13.6: A blocked top-level slug response contains no 8x8/JaaS meeting embed payload.
  - ✅ RT-13.12: A `HEAD` request, and a request with a disallowed method, to a blocked top-level slug returns the same status and headers as the `GET` inactive response, with no `Content-Length` or other header that varies by blocked state.
  - ✅ RT-13.13: A request to `/` (the empty top-level segment) without a moderator bypass returns the same inactive-room response as any other blocked slug.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC13.2 - Given a path that is not a single-segment meeting-room slug, inactive-room messaging is absent from route-specific non-room responses; in particular the `/<room>/moderator` entry response does not vary with the room's registry state, so it cannot be used as an oracle for slug validity.

- Introduced: #13 (closed 2026-08-05)
- Tests:
  - ✅ RT-13.7: `GET /room/moderator` keeps the moderator entry page and does not contain the inactive-room message.
  - ✅ RT-13.8: `GET /bad/name` keeps the invalid-room response and does not contain the inactive-room message.
  - ✅ RT-13.9: `GET /bad/name/moderator` keeps non-moderator-route handling and does not contain the inactive-room message.
  - ✅ RT-13.14: `GET /<active-room>/moderator` and `GET /<never-registered-room>/moderator` return the same moderator entry response, with no difference in status, headers, or body attributable to registry state.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC13.3 - Given public access to an active single-segment meeting-room path, the meeting page remains available and the inactive-room page is not shown; and no public response distinguishes a previously-valid slug from a never-registered one.

- Introduced: #13 (closed 2026-08-05)
- Tests:
  - ✅ RT-13.10: A guest loading a room during its active window receives the meeting page rather than the inactive-room page.
  - ✅ RT-13.11: The public response for a slug whose latest row is cancelled, or whose active window has passed, is byte-for-byte identical in status code, response headers, and body to the response for a never-registered slug.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC13.4 - Given the inactive-room page and the meeting page, both responses instruct clients not to store the response, so an expired or cancelled window cannot be replayed from a browser or proxy cache.

- Introduced: #13 (closed 2026-08-05)
- Tests:
  - ✅ RT-13.15: The inactive-room response sets `Cache-Control: no-store`.
  - ✅ RT-13.16: The meeting-page response sets `Cache-Control: no-store`.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~
