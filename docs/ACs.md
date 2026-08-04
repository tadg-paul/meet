# Acceptance Criteria

This is the canonical spec. ACs introduced from 2026-08-04 onward live here.
Pre-cutover ACs remain in their originating issues until cited or migrated.

Last migrated: AC12.8 from #12 on 2026-08-04

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
