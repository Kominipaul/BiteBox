# Security Audit Report

## 🛠️ Active Development Exceptions

### [SEC-09] Hardcoded Default Credentials Seeded on Startup
<!-- STATUS: DEV_ONLY - CONVENIENCE BYPASS -->
- **File / Path:** `internal/db/db.go` (Lines 282-313)
- **Why it's staying:** Fixed, well-known seed credentials (`admin/admin123`, `manager/manager123`, `waiter/waiter123`, `bar/bar123`, `kitchen/kitchen123`, `dj/dj123`) are documented, deliberate demo/dev behavior — see README's "Seed accounts" section and its own "Security notes" acknowledgment, plus a startup warning already in the logs. Not a fake-fix candidate: switching to random-generated passwords would break the documented onboarding flow for every fresh clone. Must be replaced with real credentials (or a forced-change flow) before any production deployment.
