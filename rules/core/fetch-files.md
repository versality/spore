# Fetching files

For known file URLs, prefer a direct fetch over `WebFetch`:
- GitHub content: `mcp__github__get_file_contents`, or `gh api` / `gh pr view` / `gh issue view`.
- Other raw URLs (GitLab, Codeberg, `raw.githubusercontent.com`, gists, pastebins): `curl -sL <url> -o <tmpfile>` then `Read`.

`WebFetch` pulls full rendered HTML and runs a summarizer LLM over it. Reserve it for pages where you genuinely need HTML->markdown conversion of a rendered view; a static file read has neither cost.
