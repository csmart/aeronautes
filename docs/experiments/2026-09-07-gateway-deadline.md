# Gateway deadline test ordering

The behaviour oracle is [ADR 0014](../adr/0014-child-storage-gateway.md): scoped gateways remain available during the ten-second SIGINT cleanup grace and revoke independently of child exit at its deadline.
The existing test allows two additional seconds for scheduling and observing closure.
This repair retains that allowance and changes only the test's synchronisation.

## Failure and reproduction

[PR42 CI](https://github.com/csmart/aeronautes/actions/runs/34070539971) passed all five required jobs before its protected merge.
The resulting [master run](https://github.com/csmart/aeronautes/actions/runs/34071011273) failed `TestGatewayCancelDeadlineRevokesWithoutChildExit` in the SAIO job with `cancel deadline left the listener usable` after 10.01 seconds.
The Go, frontend and smoke jobs succeeded; the aggregate correctly refused the failed SAIO result.
Feature delivery stopped for this repair.

At cancellation, the gateway's request context ends and the handler can send its final HTTP 502.
Go's `http.Server.Close` disables keep-alives before closing its listeners and then its active connections.
The handler can therefore close a request socket while listener shutdown is still in progress.
The test immediately dialled after observing socket closure and treated any successful connection as missed deadline revocation, although its observation allowance had not expired.

In a disposable copy of master `b5ff748465d675e9b19d0cd25a8d137eb085a49c`, the original test passes normally and reproduces the exact listener assertion when this temporary method widens the listener-close interval:

```go
func (l *gatewayListener) Close() error {
	time.Sleep(250 * time.Millisecond)
	return l.Listener.Close()
}
```

Both runs use `go test -race -count=1 -json ./internal/transfer/rclone -run '^TestGatewayCancelDeadlineRevokesWithoutChildExit$'` with a real loopback gateway and upstream request.
The delayed original fails at 10.25 seconds; the corrected test passes with the same delay.
The delay is a controlled reproduction of the shutdown ordering, not a claim about the runner's exact scheduling delay.
No delay or test hook is added to production code.

## Repair and refusal controls

Capture one absolute deadline before requesting cancellation and use it for both the bounded socket drain and listener probes.
Require EOF or a connection reset from the socket, and connection refusal from the listener; unrelated I/O errors and timeouts fail.
Retry a successful listener dial within that same bound, closing every probe connection.
Do not call invocation cleanup until both checks have completed or the test has failed.

| Controlled variation | Corrected test result |
| --- | --- |
| Listener closure delayed by 250 ms | Pass at 10.25 s |
| Deadline cancellation removed | Socket deadline assertion fails at 12 s |
| Listener closure delayed by 3 s | Listener deadline assertion fails at 12 s |
| Active-socket closure delayed by 3 s | Socket deadline assertion fails at 12 s |

The three refusal controls fail their intended assertions, without build errors, races, skips or panics.
The delayed negative controls finish their cleanup at 13 seconds; that is not an extra second allowed by the assertions.
These are test-validation controls, not additional application scenario coverage.
The gateway, child lifecycle, pipeline configuration and merge protections are unchanged.

The full internal race suite with the pinned rclone and two owned SAIO accounts passes: 504 top-level tests and 979 leaf cases across 20 packages, with only the optional `TestRcloneMatrix` skipped.
Reconciliation of the actual exit status and complete event stream is valid at 19/108 cells, with 44/49 actions still having gaps.
`go vet ./internal/...`, formatting and whitespace checks also pass.
Frontend source is unchanged; its required PR and master jobs still gate delivery.
