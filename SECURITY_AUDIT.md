# Security Audit Report

## 🛠️ Active Development Exceptions

### [SEC-09] Hardcoded Default Credentials Seeded on Startup
<!-- STATUS: DEV_ONLY - CONVENIENCE BYPASS -->
- **File / Path:** `internal/db/db.go` (Lines 282-313)
- **Why it's staying:** Fixed, well-known seed credentials (`admin/admin123`, `manager/manager123`, `waiter/waiter123`, `bar/bar123`, `kitchen/kitchen123`, `dj/dj123`) are documented, deliberate demo/dev behavior — see README's "Seed accounts" section and its own "Security notes" acknowledgment, plus a startup warning already in the logs. Not a fake-fix candidate: switching to random-generated passwords would break the documented onboarding flow for every fresh clone. Must be replaced with real credentials (or a forced-change flow) before any production deployment.

## ✅ Fixed (2026-10-02, OWASP ASVS L2 pass)

| ID | Issue | Fix |
|---|---|---|
| SEC-13 | Sequential `/table/{n}` URLs: anyone could claim any table and watch its live feed | QR codes open `/t/{token}` (96-bit random, revocable via **New code** on `/admin/map/qr`); `/table/{n}` and its websocket only serve the phone hosting that table. `TestTableClaimNeedsQRToken`, `TestTableStatusWSRejectsNonHost` |
| SEC-14 | Product `image_url` accepted `../` paths, and replacing the photo `os.Remove`d them (e.g. `bitebox.db`) | Local paths must be clean, checked on save and at the delete. `TestParseImageURLRejectsPathTraversal` |
| SEC-15 | Unauthenticated websockets had no read limit (memory DoS) | `SetReadLimit(512)`; clients never send data frames |
| SEC-16 | CSP allowed inline script | `script-src 'self'`: all page scripts moved to `static/js/`, data via `<script type="application/json">`, inline handlers became `data-*` hooks (`static/js/base.js`); `connect-src` limited to this host. `TestNoInlineScriptsInTemplates` |
| SEC-17 | No second factor; 6-character passwords; helper-set passwords never changed; password change kept other sessions; `resetpassword` set the guessable `<user>123` | Two-step login (TOTP) required for admins, optional for others, replay-proof, secrets sealed with a key outside the DB; 8+ characters with a guessable-password check; helper-set passwords are one-time; password change and two-step setup sign out other devices; `resetpassword` prints a random one-time password and resets two-step. `TestTwoStepLogin`, `TestAdminMustSetUpTwoStep`, `TestStaffResetClearsTwoStepAndForcesChange` |
| SEC-18 | Login timing revealed which usernames exist | Unknown usernames are checked against a dummy bcrypt hash |
| SEC-19 | No rate limit on call-waiter, checkout/cancel, booking confirm; behind a proxy every limit was shared by all users | Per-IP limits added; `TRUSTED_PROXIES` takes the client IP the proxy appended. `TestCallWaiterIsRateLimited`, `TestClientIPBehindTrustedProxy` |
| SEC-20 | Unauthenticated `/debug/push-client-state` wrote request bodies to the log | Removed (and its call in `sw.js`) |
| SEC-21 | Brand upload: no body cap; a failed write was ignored (shadowed `err`) | Capped like product photos; the error is checked |
| SEC-22 | Go 1.25.0 (35 reachable stdlib CVEs), old x/crypto, golang-jwt | Toolchain go1.26.8, modules updated: govulncheck reports 0 reachable |
| SEC-23 | Missing HSTS / Permissions-Policy | Added (HSTS only behind an HTTPS proxy, never on localhost) |

## ⏳ Still open

- **SEC-09** (above): seeded accounts. Whoever logs in first with `admin/admin123` picks the password *and* enrolls their own two-step phone, so change the seeded passwords before the server is reachable from any network you don't control.
- **`bitebox.db` in git history**: it was committed before `.gitignore` covered it. gitleaks finds no secrets in history, but the file holds seeded bcrypt hashes and demo data. Purging it means rewriting history (`git filter-repo --path bitebox.db --invert-paths`) and force-pushing.
- **Backups** (`/admin/backup`, `scripts/backup-db.sh`) are unencrypted copies of guest names, phones and emails. Two-step secrets inside are sealed; everything else is readable. Store them encrypted (e.g. `age`) and off the server.
- **No payment processor yet**: webhook signature/amount/idempotency checks (checklist §7) apply once one is added.
- **External pentest** before launch.
