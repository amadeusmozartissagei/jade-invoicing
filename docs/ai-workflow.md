# How the AI coding agent is used

This project is built with an AI coding agent (Claude Code). The agent writes code and documentation and does research. The owner, Hamza Pratama, sets the goals, makes the decisions, reviews the work, and must be able to explain every part.

This file records what the agent did, what the human decided, and how the work was checked. Add an entry for each significant decision or slice.

## Decision log

### 2026-11-01: Pivot from e-KYC to invoicing (ADR 0001)

- **What the agent did.** It summarised the original project rationale, proposed a free-tier stack, compared domains, and checked the current free-tier limits of Vercel, Supabase, and Hugging Face against their official documentation.
- **What the human decided.** Hamza added the constraint that the deployment must be fully free. He also questioned whether e-KYC was still the right domain under that constraint, and chose the invoicing platform.
- **How it was checked.** The agent first claimed, from memory, that Hugging Face Spaces could host the Python worker for free. When asked to verify, it found in the official documentation that Docker Spaces now require a paid plan, and it corrected the claim. Every limit in ADR 0001 links to its source, and facts that could not be confirmed (for example, whether Supabase Free includes `pgmq` and `pg_cron`) are marked as open items for a spike.
- **Lesson.** Free-tier limits and platform policies change often. The agent's memory is not a source; official documentation with a retrieval date is.

### 2026-11-01: Domain research (docs/domain/)

- **What the agent did.** It read primary sources (NTA publications and Q&A, German statutes on gesetze-im-internet.de, KoSIT and FeRD pages, the GDPR) and wrote one note per topic, with a source for each claim and a list of design implications.
- **What the human decided.** Hamza chose to do the regulatory research before the free-tier spike.
- **How it was checked.** Page references to NTA PDFs were matched to the extracted text page by page. Claims the agent could not source were removed or moved to "open questions". The BMF website blocked automated access with a bot check. The agent did not try to pass the check; it read the BMF letter from a Wayback Machine snapshot instead, recorded the snapshot URL and SHA-256, and replaced the claims that had relied on a secondary summary (IHK Köln) with citations to the letter. One of those secondary claims was imprecise and was corrected.

### 2026-11-01: Reading the BMF letters

- **What the agent did.** It found a copy of the 2024 BMF letter hosted by a public-law chamber and recorded its SHA-256, and it found that a third BMF letter (15 Oct 2026) exists. It added both to the German domain note.
- **Where it stopped.** The BMF website shows a bot check. The agent did not click through it. One attempt to download the 2026 letter directly from the BMF site was blocked by the agent's own permission system as possible bot-check circumvention, and the agent stopped trying. The owner downloads such documents by hand.
- **Follow-up.** Hamza downloaded the 2026 letter by hand. The agent read it in full, replaced the secondary summary with citations to the letter, and recorded the file's SHA-256.
- **Mapping table.** The agent downloaded the KoSIT/FeRD mapping table (mandatory items to EN 16931 fields), checked the text extraction against the rendered pages where the columns had been mixed up, and summarised it in its own words in `docs/domain/germany-en16931-mapping.md`. The BMF letter PDF was moved into `docs/domain/sources/` at Hamza's request; the KoSIT/FeRD PDF was not copied, because it is copyrighted by its publishers.

### 2026-11-01: Free-tier spike (ADR 0003)

- **What the agent did.** It wrote the throwaway spike (Go functions, a small SQL runner, cron and queue SQL), deployed it to Vercel, ran the measurements, cleaned up the cron job and test queue, and wrote ADR 0003.
- **What the human decided.** Hamza approved the spike spec, created the Supabase and Vercel accounts, ran every login and `supabase link`, and put the database connection string into `.env.local` himself. The agent never saw the password: the SQL runner and the Vercel env setup read `.env.local` without printing it, and the spike token was generated straight into that file.
- **How it was checked.** Each acceptance criterion was tested against the live services: extension versions queried from the database, queue round trip, HTTP 401 without a token, cron run history, `pg_net` response codes, and end-to-end lag. What was not measured (cold start, Active CPU per call) is stated as such in the ADR.
