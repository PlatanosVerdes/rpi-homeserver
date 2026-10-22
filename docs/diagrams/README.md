# Diagrams

Made with [archify](https://github.com/tt-a1i/archify). Each PNG has its source in `src/<name>.json`.

To change one, edit its JSON and render it from a scratch directory, so the work files stay out of the repo:

```bash
mkdir -p /tmp/diagrams && cd /tmp/diagrams
node <archify>/bin/archify.mjs finalize <type> <repo>/docs/diagrams/src/<name>.json <name>.html \
  --repo-root <repo> --quality showcase
```

`<type>` is the JSON's `diagram_type`. Export the PNG from the HTML page (Export > PNG) and replace
`docs/diagrams/<name>.png`. The `sources` in each JSON point at the commit it was drawn from.
