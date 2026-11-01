# 0002. Repository name: jade-invoicing

- **Status:** Accepted
- **Date:** 2026-11-01
- **Decided by:** Hamza Pratama (owner)

## Context

The local folder was still called `kyc-platform` after the pivot to invoicing (ADR 0001). The repository will be public and linked from the owner's CV and portfolio, so the name should say what the project is about.

## Options

- `seikyu` (請求, Japanese for billing): short, but only points to Japan.
- `invoice-jp-de`: fully descriptive, but forgettable.
- `rechnung-seikyu`: German and Japanese words for invoice; long and hard to type.
- `jade-invoicing`: JApan + DEutschland plus the domain.

## Decision

`jade-invoicing`.

## Consequences

- The name says "invoicing" outright, and "jade" gives a short handle for talks and the README.
- The meaning of "jade" is not obvious, so the README states it in its first line.
- The local folder is renamed from `C:\Projects\kyc-platform` to `C:\Projects\jade-invoicing`. Claude Code keys its per-project memory by folder path, so the next session starts with an empty memory folder; `CLAUDE.md` carries the project context, so nothing is lost.
