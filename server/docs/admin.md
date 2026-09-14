# Admin page

`/admin` is the operator's view of a signald deployment. Like the metrics
design (`metrics.md`), it is bound by the anti-surveillance stance in the
repo-root `docs/mission.md`: the operator runs the switchboard and can shut
a door, but never gains a way to listen through one. Reviewers should treat
any addition to this page as a privacy decision, not a routine UI change.

## Who can open it

Accounts whose email is listed in `ADMIN_EMAILS`. The route sits inside the
session-auth mux and then behind a `requireAdmin` check, so it inherits login,
the CSRF origin check, and the security headers before the allowlist is
consulted. Everyone else, and everyone when the list is empty, gets a 404: the
page's existence is not advertised. The Admin nav link renders only for
allowlisted users and is derived from config and the session user, never from
request input.

## What the operator can see

- Accounts: email, display name, household memberships, created date, last
  login, and whether the account is disabled.
- Households: name, member emails, line numbers, paired device count, how
  many lines are online right now, and created date.
- Devices: per line, whether it is online and the Pi and firmware versions it
  reported when it connected, flagged when behind the latest release.
- Activity: fleet-wide call counts per day for the last week, and a
  per-household call count for the same window.

Everything above is already in the database or the hub because the service
cannot run without it. The page adds no collection and no new storage other
than the `disabled_at` stamp on an account.

## What the operator never sees

- Audio, in any form. Calls are encrypted between the two phones; the server
  never has the stream.
- Who called whom. No caller and callee pairs, no per-call rows, no call
  detail links.
- Activity for a household that has call history turned off. The household
  table shows a dash instead of a count. The operator view is a subset of what
  the household chose to keep for itself, never more.
- IP or LAN addresses of devices or browsers.
- Contact graphs, links between households, or invite state.
- Session tokens, magic links, or OAuth identities.

The fleet-wide per-day table is the one activity figure that cannot be
attributed to a household, which is why it is shown regardless of opt-outs.

## Disabling an account

`POST /admin/accounts/{id}/disable` stamps `users.disabled_at` and deletes
every session for that user in the same transaction, so they are signed out
everywhere at once. Session creation is a single conditional insert on the
account being enabled, so every login path (magic link, Google, dev session)
is refused in one place and lands on the login page with an "account
disabled" message. The session middleware also rejects a disabled user whose
session somehow survived, and clears the cookie.

`POST /admin/accounts/{id}/enable` clears the stamp. Nothing else changes.

Disabling is about web access. Phones belong to households, not accounts,
and keep working. An admin cannot disable their own account. Each change is
logged with the admin's and the target's user ids and nothing else.

## Adding to this page

Before adding a column or a panel, ask the question `metrics.md` asks: if a
parent worried about their kid's privacy saw this on the operator's screen,
would they be uncomfortable? If the answer is yes, or if the new data would
show the operator something the household cannot see about itself, do not
add it.
