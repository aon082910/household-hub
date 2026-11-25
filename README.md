# Household Hub

Shared household finances and calendar. Bills with due dates, accounts, transactions, monthly budgets, and a family calendar for appointments and due dates. Everyone in the house has their own login. Reminders go out as in-app alerts, Windows notifications, browser pop-ups, phone push (ntfy), email, text message, or Discord/webhook.

One Go codebase, two versions:

| Version | What runs | Data |
|---|---|---|
| Unraid / Docker | `hhub serve` in a container (port 8080 inside, 8095 outside) | `/data/household.db` (appdata) |
| Windows | `HouseholdHub.exe` (window) + `hhub.exe background` (starts at sign-in) | Host mode: `%LOCALAPPDATA%\HouseholdHub\data\household.db`. Connect mode: on the Unraid server |

Windows modes (chosen on first start, changeable in Settings > My profile > Change server):
- **Connect**: the window shows the Unraid server; the background process shows Windows notifications for new alerts.
- **Host**: this PC is the server (127.0.0.1:8787, or 0.0.0.0 when "let phones use it" is ticked; Windows may ask to allow it through the firewall).

## Features

- Members with roles: admin (everything), adult (money + calendar), kid (calendar only). Any item can be private (only its owner sees it).
- Bills: one-time, daily, weekly, monthly, yearly, every N; month-end dates clamp (31st becomes Feb 28). Mark paid (optionally records the expense), undo, autopay (marks itself paid on the due day), payment website link, assigned person.
- Money: accounts with running balance, transactions in/out, categories, CSV export.
- Budget: monthly budget per category with progress bars.
- Calendar: month grid and 60-day list, repeats, multi-day events, who it is for (colors per person), reminders (at start, 15 min ... 1 week, custom). Bill due dates appear on it. Private `.ics` feed per person for phone/Outlook calendars.
- Reminders: engine runs every minute, each reminder is sent exactly once (sent-log keyed by item, date, lead time and person), even across restarts. Overdue bill reminders the day after. Missed reminders up to 6 hours old (events) or 20 hours (bills) still go out after a restart.
- Notification channels per person: bell, Windows/browser pop-ups, ntfy, email (SMTP), SMS (Twilio or carrier email-to-text), webhook/Discord. Household-wide webhook for shared reminders.
- Backup download (admin).

## Security notes

- Passwords: bcrypt. Sessions: random token, HttpOnly SameSite cookie, 30 days. Writes need the `X-HHub` header and a same-origin `Origin` (blocks cross-site requests). 8 failed sign-ins per IP in 10 minutes = 10-minute lock.
- Windows notifiers use device tokens that can only read and acknowledge alerts; revoke them in Settings > My notifications.
- SMTP/Twilio/ntfy secrets are typed into Settings > Household and never sent back to the browser.
- Browser pop-ups need https:// (or localhost). Over plain http on the LAN use ntfy or the Windows app.
- Not meant for the open internet without a reverse proxy with HTTPS.

## Build

`.\build.ps1` = vet, tests, `hhub.exe`, `HouseholdHub.exe`, `dist\hhub-linux-amd64`.
`-Install` copies to `D:\AI Tools\Household Hub\`, makes Start Menu, Desktop and Startup shortcuts, starts the notifier.
`-Docker` builds `household-hub:dev` and recreates `household-hub-dev` on http://localhost:8095 (volume `household-hub-data`).
Icons: `python tools\make_icons.py`.

## Install

**Unraid:** add `https://github.com/aon082910/AoN-Unraid-Apps` under Apps, Settings, Template Repositories, then search for Household-Hub. Or import `unraid/household-hub.xml`.

**Docker:**
```bash
docker run -d --name household-hub -p 8095:8080 -e TZ=America/New_York \
  -v /mnt/user/appdata/household-hub:/data allornothing/household-hub:latest
```
Open `http://<server>:8095/` and create the first (admin) account.

**Windows:** build with `.\build.ps1 -Install`, then open Household Hub from the Start Menu.

Image: [allornothing/household-hub](https://hub.docker.com/r/allornothing/household-hub) · MIT licensed.
