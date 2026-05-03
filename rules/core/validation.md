## Validation

Spore self-validates with the same lint set it ships: drift,
agent mirrors, file-size, comment-noise, em-dash. Run `spore lint` plus
`go test ./...` before push; both must be green.
