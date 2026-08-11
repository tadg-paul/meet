# Acceptance Criteria

This is the canonical spec. ACs introduced from 2026-08-04 onward live here.
Pre-cutover ACs remain in their originating issues until cited or migrated.

Last migrated: AC18.5 from #18 on 2026-08-11

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
- Superseded by AC14.1 (#14, closed 2026-08-11): the `/<room>/moderator` entry response is now active-gated and does vary with registry state, returning the guest inactive-room 404 unless the room is active. The oracle-resistance goal is preserved by AC14.1 (blocked states are byte-identical to the guest 404); it is no longer achieved by serving the entry page unconditionally.
- Tests:
  - ~~🚫 RT-13.7: `GET /room/moderator` keeps the moderator entry page and does not contain the inactive-room message.~~ (removed: superseded by #14)
  - ✅ RT-13.8: `GET /bad/name` keeps the invalid-room response and does not contain the inactive-room message.
  - ✅ RT-13.9: `GET /bad/name/moderator` keeps non-moderator-route handling and does not contain the inactive-room message.
  - ~~🚫 RT-13.14: `GET /<active-room>/moderator` and `GET /<never-registered-room>/moderator` return the same moderator entry response, with no difference in status, headers, or body attributable to registry state.~~ (removed: superseded by #14)

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

### AC14.1 - Given a request to `/<room>/moderator`, the moderator login form is available only when the room's latest registry row is `created` and the current time is within its window; for any not-active room (unknown, before-window, after-window, cancelled) the response is byte-identical to the guest inactive-room 404, disclosing no more about the slug than the plain room URL and never distinguishing a scheduled-but-not-open room from a never-registered name.

- Introduced: #14 (closed 2026-08-11). Supersedes AC13.2.
- Tests:
  - ✅ RT-14.1: An active room's moderator path serves the login form.
  - ✅ RT-14.2: An unknown slug's moderator path returns the inactive 404.
  - ✅ RT-14.3: A before-window room's moderator path returns the inactive 404.
  - ✅ RT-14.4: An after-window room's moderator path returns the inactive 404.
  - ✅ RT-14.5: A cancelled room's moderator path returns the inactive 404.
  - ✅ RT-14.6: The moderator-path responses for unknown, before-window, after-window, and cancelled rooms are byte-for-byte identical.
  - ✅ RT-14.7: For the same not-active room, the moderator path and the plain room path return the byte-identical inactive page.
  - ✅ RT-14.8: A form submission to a not-active room's moderator path delivers no magic link and returns the inactive 404.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC14.2 - Given the public moderator-auth pages, the entry page heading and title are "Login", and no public moderator-auth page copy contains the word "moderator".

- Introduced: #14 (closed 2026-08-11)
- Tests:
  - ✅ RT-14.9: The entry page heading and title are "Login" and the body contains neither "Moderator" nor "moderator".
  - ✅ RT-14.10: The failed-verify page copy contains no "moderator".
  - ✅ RT-14.11: The check-email page copy contains no "moderator".

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

---

## Meeting Timer

UT-15.1 through UT-15.14 were confirmed passing by the operator on 2026-08-08.

### AC15.1 - Given a timer configuration of total time, early-warning percent, and grace percent, the room stores the configuration and its derived early-warning threshold and grace limit in whole seconds rounded to the nearest second; a configuration whose total is not positive, or whose early-warning or grace percent falls outside 0 to 100, is not stored.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ RT-15.1: A 5:00 total with a 10% early-warning yields a threshold 30s before the total.
  - ✅ RT-15.2: A 15:00 total with a 30% grace yields a 4:30 grace limit.
  - ✅ RT-15.3: A percentage that does not divide evenly is rounded to the nearest second.
  - ✅ RT-15.4: A total of zero or negative leaves no stored configuration.
  - ✅ RT-15.5: An early-warning or grace percent below 0 or above 100 leaves no stored configuration.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.2 - Given a started timer, the room's reported phase is running-before-warning while run-elapsed is under the early-warning threshold, running-after-warning from the threshold until the total, grace (counting up from zero) from the total until the grace limit, and grace-exceeded once the grace limit passes; reported remaining time before the total and reported count-up after the total track the clock.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ RT-15.6: Just before the threshold, the phase is running-before-warning.
  - ✅ RT-15.7: At and after the threshold but before the total, the phase is running-after-warning.
  - ✅ RT-15.8: At the total, the phase is grace with a count-up of zero.
  - ✅ RT-15.9: Within grace, the reported count-up matches elapsed-since-total.
  - ✅ RT-15.10: At the grace limit, the phase is grace-exceeded.
  - ✅ RT-15.11: Reported remaining before the total matches total-minus-elapsed.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.3 - Given a paused timer, run-elapsed does not advance while wall-clock time passes, and resuming continues from the frozen run-elapsed.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ RT-15.12: After a pause, a wall-clock advance leaves the phase and remaining unchanged.
  - ✅ RT-15.13: After a resume, a wall-clock advance progresses the phase and remaining from the frozen point.
  - ✅ RT-15.14: A pause in the running-after-warning phase holds that phase across a wall-clock advance.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.4 - After a reset or stop, the room has no active timer and retains its configuration; after a restart, the retained configuration runs from zero run-elapsed.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ RT-15.15: After a reset, the room is stopped and a subsequent start reuses the retained settings.
  - ✅ RT-15.16: After a restart, the timer runs from zero with the same configuration.
  - ✅ RT-15.17: A stop from the grace phase leaves the room stopped.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.5 - Given a timer that reaches its grace limit, without an extend action the room is grace-exceeded and, after the ten-second flash window, stopped; an extend action taken during grace keeps the count-up active past the grace limit until a stop.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ RT-15.18: Reaching the grace limit without an extend yields grace-exceeded, then stopped after the flash window.
  - ✅ RT-15.19: An extend during grace keeps the timer active and counting past the grace limit.
  - ✅ RT-15.20: A stop after an extend leaves the room stopped.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.6 - A timer control action (set, start, pause, resume, reset, restart, stop, extend) changes the room's timer only when the request carries a valid moderator token scoped to that room or a wildcard token; without one, the timer state is unchanged.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ RT-15.21: A control action with no token leaves the state unchanged.
  - ✅ RT-15.22: A control action with an invalid or expired token leaves the state unchanged.
  - ✅ RT-15.23: A control action with a room-scoped token for a different room leaves the state unchanged.
  - ✅ RT-15.24: A control action with a room-scoped token for this room is applied.
  - ✅ RT-15.25: A control action with a wildcard super-moderator token is applied.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.7 - A participant's timer subscription reflects the room's current timer state on connect and each subsequent state change; a subscription opened mid-timer reflects the in-progress phase and remaining, not a stopped state.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ RT-15.26: A new subscription carries the current state as its first message.
  - ✅ RT-15.27: A subscription opened after the timer started carries the in-progress phase, not a stopped state.
  - ✅ RT-15.28: A pause applied after a subscription opens is carried to that subscription.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.8 - A room holds a single timer whose state reflects the most recently applied control action, and every subscriber to that room observes the same state.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ RT-15.29: Two sequential control actions from different moderator tokens converge to the later action's state.
  - ✅ RT-15.30: Two concurrent subscriptions observe identical post-action state.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.9 - Timers are independent per room: a control action on one room's timer leaves other rooms' timers unchanged.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ RT-15.31: Starting a timer in one room leaves a second room with no active timer.
  - ✅ RT-15.32: Two rooms run timers at independent phases concurrently.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.10 - The banner colour reflects the timer phase for every participant: blue when no timer is active, green before the early-warning, amber after the early-warning, red during grace, and black alternating with red about once per second for ten seconds once grace is exceeded, returning to blue on reset; numerals stay legible against each colour.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ UT-15.1: A participant observes the banner move blue to green to amber to red across a run, with legible numerals throughout.
  - ✅ UT-15.2: A participant observes the grace-exceeded black/red flash for about ten seconds and the return to blue on auto-reset.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.11 - The banner presents moderators the phase-appropriate controls (stopped: Set, Start; running: Pause; paused: Resume, Reset, Restart; grace: Extend, Stop) and presents non-moderators no controls and numerals only while a timer is active, including the grace count-up.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ UT-15.3: A moderator sees the correct control set in each phase.
  - ✅ UT-15.4: A non-moderator sees the countdown and grace count-up but never any control.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.12 - Every participant hears a start chime when the timer starts, a pause chime when it is paused, a resume chime when it resumes, the early-warning ding on entering the amber phase, the end chime at the total, and the end-of-grace chime when the grace limit passes, with audio armed on join so the first cue is not suppressed by the browser.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ UT-15.5: All participants hear the start chime, warning ding, end chime, and end-of-grace chime at the correct moments across a run.
  - ✅ UT-15.6: A participant who has joined the meeting hears the start chime rather than having it blocked by autoplay policy.
  - ✅ UT-15.14: All participants hear the pause chime when the timer is paused and the resume chime when it is resumed.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.13 - The configuration control presents total as mm:ss and early-warning and grace as percentages, and shows the mm:ss computed from each percentage, updating as the values change.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ UT-15.7: On opening, the popover shows the correct computed mm:ss beside the early-warning and grace percentages for the current total.
  - ✅ UT-15.8: Editing the total or either percentage updates the computed mm:ss live.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.14 - On a narrow (mobile) viewport the active timer and its controls remain usable without overflowing the banner or obscuring the domain.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ UT-15.9: The banner with an active timer and the moderator control set remains usable at a phone width.
  - ✅ UT-15.10: The non-moderator banner with an active timer remains usable at a phone width without obscuring the domain.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.15 - While any of the six chimes plays, a participant whose microphone is unmuted has it muted for that chime's duration and restored to unmuted when the chime ends; a participant whose microphone is already muted stays muted throughout.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ UT-15.11: With an unmuted microphone, a participant's microphone mutes for the duration of a chime and returns to unmuted after it ends.
  - ✅ UT-15.12: With an already-muted microphone, the microphone stays muted across a chime and is not unmuted.
  - ✅ UT-15.13: On a second participant's device, a chime playing on the first participant's device is not heard re-broadcast into the meeting.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC15.16 - A room's timer configuration persists across a server restart: after a fresh server construction the room's stored configuration is the one last set, and a room with no configuration set has the system defaults. The live running state (running, paused, elapsed) does not persist and is cleared by a restart.

- Introduced: #15 (closed 2026-08-08)
- Tests:
  - ✅ RT-15.33: After a configuration is set and the server is reconstructed, the room's configuration matches the last set values.
  - ✅ RT-15.34: A later configuration set supersedes an earlier one across a reconstruction (last-row-wins).
  - ✅ RT-15.35: A room with no configuration set has the system defaults after a reconstruction.
  - ✅ RT-15.36: After a reconstruction, a room that had a running timer has no active timer but retains its configuration.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

---

## Recurring Schedules

### AC17.1 - The room registry stores recurring meeting definitions (weekly, every-N-weeks, monthly Nth-weekday), and the latest row for a room remains its authoritative definition under last-row-wins, whether that row is a one-off window, a recurring definition, or a cancellation.

- Introduced: #17 (closed 2026-08-11)
- Tests:
  - ✅ RT-17.1: A stored weekly definition is read back as the room's current definition.
  - ✅ RT-17.2: A stored monthly Nth-weekday definition is read back as the room's current definition.
  - ✅ RT-17.3: A later definition for the same room supersedes an earlier one.
  - ✅ RT-17.4: A cancellation supersedes a recurring definition.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC17.2 - Given a weekly or every-N-weeks recurring room, guest access is open during each occurrence's active window and closed between occurrences, with every-N-weeks leaving the intervening weeks closed.

- Introduced: #17 (closed 2026-08-11)
- Tests:
  - ✅ RT-17.5: A guest loads the meeting page during the first occurrence's window.
  - ✅ RT-17.6: A guest receives the inactive page between the first and next occurrence.
  - ✅ RT-17.7: A guest loads the meeting page during a later weekly occurrence.
  - ✅ RT-17.8: For a fortnightly room, a guest receives the inactive page during the skipped week and the meeting page on the following occurrence.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC17.3 - Given a monthly Nth-weekday recurring room, guest access is open during that occurrence each month, and a month with no Nth instance of the weekday has no occurrence.

- Introduced: #17 (closed 2026-08-11)
- Tests:
  - ✅ RT-17.9: A "first Wednesday, 18:00 UTC" room opens to a guest during the first Wednesday's window.
  - ✅ RT-17.10: A "second Tuesday, 20:30 UTC" room opens to a guest during the second Tuesday's window.
  - ✅ RT-17.11: A month whose Nth weekday is absent yields no occurrence and the guest receives the inactive page.
  - ✅ RT-17.12: Occurrences in adjacent months are computed independently at the correct dates.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC17.4 - When a room does not specify occurrence duration or early-open lead, the create subcommand resolves them from the application config YAML defaults and records them on the row; explicit `--duration`/`--open-early` values override the config defaults.

- Introduced: #17 (closed 2026-08-11)
- Tests:
  - ✅ RT-17.13: A create without `--duration` records the config-default duration on the row.
  - ✅ RT-17.14: A create without `--open-early` records the config-default lead on the row.
  - ✅ RT-17.15: An explicit `--duration` overrides the config default on the recorded row.
  - ✅ RT-17.16: An explicit `--open-early` overrides the config default on the recorded row.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC17.5 - An occurrence's active window opens exactly the lead before its start and closes exactly at start-plus-duration, with inclusive boundaries.

- Introduced: #17 (closed 2026-08-11)
- Tests:
  - ✅ RT-17.17: At exactly start-minus-lead, a guest loads the meeting page.
  - ✅ RT-17.18: One second before start-minus-lead, a guest receives the inactive page.
  - ✅ RT-17.19: At exactly start-plus-duration, a guest loads the meeting page.
  - ✅ RT-17.20: One second after start-plus-duration, a guest receives the inactive page.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC17.6 - A recurring definition with a series end (`--ends`) has no occurrences after that date; without one the series continues indefinitely.

- Introduced: #17 (closed 2026-08-11)
- Tests:
  - ✅ RT-17.21: An occurrence before the series end opens to a guest.
  - ✅ RT-17.22: A would-be occurrence after the series end gives the guest the inactive page.
  - ✅ RT-17.23: With no series end, an occurrence far in the future still opens to a guest.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC17.7 - A cancellation of a recurring room closes guest access during would-be occurrences, and a subsequent recurring definition re-opens it.

- Introduced: #17 (closed 2026-08-11)
- Tests:
  - ✅ RT-17.24: After cancelling a recurring room, a guest receives the inactive page during what would have been an active occurrence.
  - ✅ RT-17.25: A recurring definition created after a cancellation re-opens guest access during an occurrence.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC17.8 - The create subcommand records a recurring room from recurrence, `--duration`, `--open-early`, and `--ends` flags, rejecting invalid definitions (unknown weekday, out-of-range ordinal, unparseable duration, missing required fields) and writing no row on rejection; the deprecated `--until` remains accepted for one-off rooms.

- Introduced: #17 (closed 2026-08-11)
- Note: the closing clause "the deprecated `--until` remains accepted for one-off rooms" was superseded by #20 (closed 2026-08-11); `--until` is now rejected outright and one-off length comes from `--duration`. RT-17.33 was repurposed accordingly.
- Tests:
  - ✅ RT-17.26: A create with a weekly recurrence writes a recurring row.
  - ✅ RT-17.27: A create with a monthly Nth-weekday recurrence writes a recurring row.
  - ✅ RT-17.28: A create with an unknown weekday exits non-zero and writes no row.
  - ✅ RT-17.29: A create with an out-of-range ordinal exits non-zero and writes no row.
  - ✅ RT-17.32: A create with `--ends` records the series end on the row.
  - ✅ RT-17.33: A create passing the removed `--until` flag is rejected and writes no row. (Repurposed by #20; originally asserted a one-off `--until` recorded the correct window.)

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC17.9 - One-off room windows continue to admit a guest only within their configured window.

- Introduced: #17 (closed 2026-08-11)
- Tests:
  - ✅ RT-17.30: A one-off room opens to a guest within its window.
  - ✅ RT-17.31: A one-off room gives the guest the inactive page outside its window.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC17.10 - The end-to-end schedule works against the live deployment: an operator-defined recurring room admits a participant during a scheduled occurrence and refuses outside it.

- Introduced: #17 (closed 2026-08-11)
- Tests:
  - ✅ UT-17.1: The operator runs `create` with a recurrence on the deployed host; a second device reaches the meeting during a current occurrence and the inactive page outside it. (Operator-confirmed PASS 2026-08-11.)

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC17.11 - A duration value is a number, or a colon-split pair, followed by a unit suffix (`h`/`hour`, `m`/`min`, `s`/`sec`), where the colon splits the value into that unit and the next-smaller one; malformed, unit-less, or non-positive values are rejected.

- Introduced: #17 (closed 2026-08-11)
- Tests:
  - ✅ RT-17.34: `4:30h` parses to four hours thirty minutes.
  - ✅ RT-17.35: `90:00 min` parses to ninety minutes.
  - ✅ RT-17.36: `4h`, `30m`, and `45s`, and their `hour`/`min`/`sec` synonyms, parse to the plain unit values.
  - ✅ RT-17.37: A value with no unit, a non-numeric value, or a non-positive value is rejected.
  - ✅ RT-17.38: A colon minor field of sixty or more is rejected.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC18.1 - A recurring definition may carry an IANA timezone; its occurrence start times are computed in that zone and compared against the current instant in UTC, and a definition with no timezone is computed in UTC as in #17.

- Introduced: #18 (closed 2026-08-11)
- Tests:
  - ✅ RT-18.1: A definition with a timezone is stored and read back carrying that zone.
  - ✅ RT-18.2: A guest loads the meeting page during an occurrence whose in-zone start maps to the current UTC instant.
  - ✅ RT-18.3: A definition with no timezone admits a guest at the same instants as the equivalent UTC definition.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC18.2 - Occurrences observe daylight saving: a fixed local time-of-day maps to different UTC instants on either side of a DST transition for the definition's zone, and the guest window tracks the shift.

- Introduced: #18 (closed 2026-08-11)
- Tests:
  - ✅ RT-18.4: For a zoned fixed-local-time definition, an occurrence in standard time and one in daylight time have UTC starts that differ by the zone's offset change.
  - ✅ RT-18.5: A guest is admitted at the correct UTC instant for a post-transition occurrence, not at the pre-transition UTC instant.
  - ✅ RT-18.10: A monthly `--at` time is interpreted in the `--tz` zone, so the stored anchor is the correct UTC instant (15:30 America/Detroit EDT is 19:30Z). (Added as a regression during #18.)

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC18.3 - Daylight-saving boundary edge cases resolve deterministically: a local time that does not occur on the spring-forward day, and one that occurs twice on the fall-back day, each yield a single defined occurrence rather than an error.

- Introduced: #18 (closed 2026-08-11)
- Tests:
  - ✅ RT-18.6: A definition whose local time falls in the spring-forward gap yields a defined occurrence rather than an error.
  - ✅ RT-18.7: A definition whose local time falls in the fall-back overlap yields a single defined occurrence.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC18.4 - The create subcommand accepts and validates a timezone, rejecting an unknown zone name and writing no row.

- Introduced: #18 (closed 2026-08-11)
- Tests:
  - ✅ RT-18.8: A create with a valid IANA zone writes a row carrying that zone.
  - ✅ RT-18.9: A create with an unknown zone name exits non-zero and writes no row.

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~

### AC18.5 - The end-to-end zoned schedule works against the live deployment: a zoned recurring meeting opens at the correct local wall-clock time.

- Introduced: #18 (closed 2026-08-11)
- Tests:
  - ✅ UT-18.1: The operator schedules a zoned recurring room on the deployed host; a second device reaches the meeting at the correct local time during a current occurrence and the inactive page outside it. (Operator-confirmed PASS 2026-08-11.)

**Key:** ✅ passing · ⏳ pending · ❌ failing · ~~🚫 removed~~
