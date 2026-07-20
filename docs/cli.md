# CLI reference

```text
regbot validate --config FILE
regbot plan --config FILE [--output table|json] [--out FILE]
regbot apply --config FILE --plan FILE [--output table|json]
regbot run --config FILE [--output table|json]
regbot serve --config FILE [--listen ADDRESS] [--run-token-env NAME|--run-token-file FILE]
regbot healthcheck [--url URL] [--timeout DURATION]
regbot version
```

Global flags:

- `--config`, `-c`: configuration path; default `regbot.yaml`
- `--log-format`: `text` or `json`
- `--log-level`: `debug`, `info`, `warn`, or `error`

Exit codes:

| Code | Meaning |
| --- | --- |
| `0` | Success, including no deletions. |
| `1` | Provider, network, or unexpected runtime failure. |
| `2` | Configuration, arguments, or plan decoding failure. |
| `4` | Safety limit rejected the plan. |
| `5` | Expired or mismatched plan. |
| `6` | Partial apply failure. |

Structured command output goes to stdout. Logs and errors go to stderr.
