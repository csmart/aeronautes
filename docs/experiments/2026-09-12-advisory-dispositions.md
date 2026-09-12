# Engine advisory dispositions proposed for review

## Scope and oracle

This record turns the private per-advisory disposition proposal of 9 September into a reviewable repository record for the 42 advisory identifiers reported by the retained scanner on all six accepted rclone 1.74.3 sidecars, against the application source at `f88114958c7f5211b8953e0c757687146a4c4f5d` and the public engine source at revision `37e4117f48bdef0fa1ba27f6467d7a891b0b55ba`.
The oracle is SEC-11 and P31 in the audit register: every retained advisory needs an evidence-backed disposition for the exact Swift and local surfaces and the shipped binary, and no disposition is accepted without independent review.
Every row keeps its draft class; nothing is reclassified, no advisory trigger was executed, no candidate engine was run, and no row is an accepted disposition, an exposure assertion or a closure.
The [structured record](2026-09-12-advisory-dispositions.json) carries, for every row, the observed modules, the six-target symbol presence, the primary references, the digests of the retained advisory entries and the exact source citations with their retained line text and file digests, together with the digests of the frozen draft and the public engine source files it cites.

## Classes

| Class | Rows | Meaning |
| --- | ---: | --- |
| Unknown: shared input | 2 | a shared input-processing path needs deeper caller and input closure |
| Unknown: transitive caller | 10 | the inspected enabled route does not establish a trigger, but the linked caller and input mapping is incomplete |
| Conditional: not enabled | 29 | the advisory's described prerequisite is absent under the inspected fixed route and configuration contract; exact-binary controls and independent review remain required |
| 32-bit prerequisite absent | 1 | the advisory describes 32-bit decoding while every retained binary reports amd64 or arm64 |

"Enabled input" means data admitted by the inspected application or source contract, such as object names, object bytes or response headers; it does not mean a vulnerable function was observed consuming that input at runtime.

## Rows

| Advisory | Observed module | Class | Input and route reasoning | Citations | Next check |
| --- | --- | --- | --- | --- | --- |
| [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) | `stdlib` | Unknown: shared input | **Input:** Relative URL references, especially redirect Location values.. HTTP client symbols are present. Gateway rejects 3xx and strips Location; ncw/swift request assembly uses package url.Parse, not affected URL.Parse. This excludes the inspected ordinary redirect path but does not close every linked URLJoin/ResolveReference caller or prove six-binary input reachability. | A, N, S, H | First priority: close remaining URL-reference callers; retain bounded long-reference and redirect refusal controls on every candidate binary. |
| [GO-2026-5970](https://pkg.go.dev/vuln/GO-2026-5970) | `golang.org/x/text` | Unknown: shared input | **Input:** Invalid UTF-8 reaching normalization of selected or backend-derived names.. Shared copy traversal installs NFC normalization, so this is not an unrelated backend finding. files-from bypasses one filter branch; local upload and recorded download validate UTF-8. Other copy paths, backend encoding conversion and malformed-response handling have not been closed through the affected iterator. | A, B, D, U | Trace normalization callers and encoding conversions across all routes; require malformed-name refusal and valid-Unicode compatibility controls. |
| [GO-2026-4970](https://pkg.go.dev/vuln/GO-2026-4970) | `stdlib` | Unknown: transitive caller | **Input:** Unix os.Root open with a final symlink and trailing slash.. Linux/Darwin affected Root symbols are present. Inspected local copy uses ordinary file opens and mkdir, not os.Root; that is not a containment guarantee. Other linked Root callers remain untraced. The advisory states Unix, so the described OS prerequisite is absent for the two Windows targets. | D, Y | Map every linked Root caller on Unix; keep separate P18 symlink/rename/cleanup hold regardless of this advisory. |
| [GO-2026-5061](https://pkg.go.dev/vuln/GO-2026-5061) | `golang.org/x/image` | Unknown: transitive caller | **Input:** Malformed WebP dimensions delivered to a decoder.. Object bytes reach the copy path, but inspected Swift/local copying and MIME fallback do not decode images. No evidence yet connects the retained WebP decoder symbols to an enabled operation, and transitive decoder callers were not fully inspected. | A, B, D, S | Identify decoder registration/call owners and prove transfer bytes cannot invoke them, or test the patched candidate decoder. |
| [GO-2026-5062](https://pkg.go.dev/vuln/GO-2026-5062) | `golang.org/x/image` | Unknown: transitive caller | **Input:** TIFF with oversized tiles delivered to a decoder.. Untrusted object bytes are enabled inputs. Copy and extension-based MIME handling do not themselves select TIFF decoding; linked decoder callers remain unresolved. | A, B, D, S | Close transitive TIFF decoder callers; candidate must contain the fixed image module or retain a reviewed exclusion. |
| [GO-2026-5066](https://pkg.go.dev/vuln/GO-2026-5066) | `golang.org/x/image` | Unknown: transitive caller | **Input:** TIFF with invalid strip offsets delivered to a decoder.. Binary symbol presence does not show that copying a TIFF invokes its decoder. The inspected transfer path moves bytes, while any transitive image helper route is still unknown. | A, B, D, S | Use the same decoder-owner inventory as 5062, retaining this distinct panic condition and advisory ID. |
| [GO-2026-5158](https://pkg.go.dev/vuln/GO-2026-5158) | `go.opentelemetry.io/otel` | Unknown: transitive caller | **Input:** Oversized baggage header passed to OpenTelemetry propagation parsing.. Gateway forwards permitted response headers under a 32 KiB header limit, but neither gateway nor inspected ncw/swift/fshttp transport extracts baggage. That cap is not a parser-exclusion proof; the linked OpenTelemetry caller graph remains incomplete. | A, B, N, S | Find Baggage.Extract/Parse owners and startup hooks; determine whether any enabled request or response path supplies their input. |
| [GO-2026-5841](https://pkg.go.dev/vuln/GO-2026-5841) | `github.com/klauspost/compress` | Unknown: transitive caller | **Input:** Attacker-controlled s2 dictionary supplied to NewDict and encoding.. The ordinary copy stream does not create an s2 dictionary. No configured compression/dictionary mode is exposed, but the retained NewDict caller in transitive dependencies was not identified; copying arbitrary bytes alone is not proof of dictionary parsing. | A, B, D | Identify the linked dictionary caller and its input source; require a fixed compress module or reviewed unreachable-input evidence. |
| [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) | `golang.org/x/crypto` | Unknown: transitive caller | **Input:** Data processed by deprecated golang.org/x/crypto/openpgp.. The advisory has no fixed version. Swift/local expose no OpenPGP operation; rclone self-update instead imports ProtonMail and is not invoked. This does not explain or exclude the different deprecated package actually reported in the binaries. | A, B, D, T | Identify its transitive owner; remove/replace it in a candidate or produce a narrowly reviewed route exclusion. A version bump alone is insufficient. |
| [GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972) | `stdlib` | Unknown: transitive caller | **Input:** Deep recursive ASN.1 decoded by affected Unmarshal.. Child Swift HTTP is plaintext loopback; upstream certificates are parsed in the Go 1.27.1 parent source configuration. No child client-certificate/key flag is exposed. Other linked ASN.1 callers and local credential/config initialization are not fully closed. | A, B, N, S | Trace non-TLS ASN.1 callers; inspect candidate toolchain metadata and add the required trust/CA compatibility cases. |
| [GO-2026-6088](https://pkg.go.dev/vuln/GO-2026-6088) | `stdlib` | Unknown: transitive caller | **Input:** Deep XML through DecodeElement/custom unmarshalling.. Swift listings explicitly use JSON and object bytes remain byte streams. Supplying XML content does not by itself select encoding/xml. The general binary still has XML symbols and its transitive decode entry points have not been fully excluded. | A, B, D, S | Identify XML decoder owners and any enabled extension/error path; retain malformed-response and opaque-object-byte controls. |
| [GO-2026-6222](https://pkg.go.dev/vuln/GO-2026-6222) | `golang.org/x/image` | Unknown: transitive caller | **Input:** Crafted VP8L data supplied to decoder Huffman allocation.. The transfer/MIME path does not decode VP8L, but linked decoder callers remain unknown. This is a distinct memory-allocation condition from the other WebP reports. | A, B, D, S | Close the decoder owner/input mapping and inspect candidate x/image version, preserving this ID separately. |
| [GO-2026-4961](https://pkg.go.dev/vuln/GO-2026-4961) | `golang.org/x/image` | 32-bit prerequisite absent | **Input:** Large WebP decoding on a 32-bit process.. All six retained build metadata records are amd64 or arm64. The advisory explicitly requires 32-bit execution, which these six exact sidecars do not provide. This says nothing about the other image advisories or future additional targets. | A, D | Keep per-artifact architecture checks; reopen if a 32-bit artifact or execution mode is introduced. |
| [GO-2026-5026](https://pkg.go.dev/vuln/GO-2026-5026) | `stdlib` | Conditional: not enabled | **Input:** Punycode hostname canonicalization used for a privilege decision.. The affected standard-library HTTP symbols run in a child whose remote authority is a generated literal 127.0.0.1 address. Original hostnames stay in the parent and upstream redirects do not reach the child. This is a conditional hostname-input exclusion, not proof that all HTTP symbols are unreachable. | A, B, N, S | Preserve literal-host construction, redirect refusal and absent proxy/remote overrides on every shipped binary. |
| [GO-2026-5051](https://pkg.go.dev/vuln/GO-2026-5051) | `github.com/cloudsoda/go-smb2` | Conditional: not enabled | **Input:** Crafted SMB QUERY_DIRECTORY response.. Fixed remote types select Swift; absolute local paths select local. The SMB constructor and cloudsoda network session are separate and not selected. A Windows UNC local path uses OS file APIs, which is not this Go SMB client; OS SMB safety is outside this claim. | A, B, I, Y | Prove backend-selector confinement and keep native UNC/platform limits separate from the Go SMB advisory. |
| [GO-2026-5774](https://pkg.go.dev/vuln/GO-2026-5774) | `github.com/go-chi/chi/v5` | Conditional: not enabled | **Input:** Untrusted forwarded address passed through chi RealIP.. No child serve command is selected; RC/metrics listener starts are guarded by absent options. The actual parent gateway installs its own net/http handler and does not use chi RealIP. Header presence alone does not enable this middleware. | A, B, C, N | Retain the no-child-server/default-config boundary; reassess if serving, RC or a forwarding-header trust policy is added. |
| [GO-2026-5775](https://pkg.go.dev/vuln/GO-2026-5775) | `github.com/go-chi/chi/v5` | Conditional: not enabled | **Input:** Unvalidated X-Forwarded-For used by chi RealIP.. Same inspected server-role exclusion as 5774. Keep this ID separate because the scanner inventory retains it; no duplicate-ID collapse is used to claim remediation. | A, B, C, N | Validate guarded listener startup and parent handler identity with the exact candidate. |
| [GO-2026-5777](https://pkg.go.dev/vuln/GO-2026-5777) | `github.com/go-chi/chi/v5` | Conditional: not enabled | **Input:** Spoofed forwarding header reaches chi RealIP middleware.. The selected commands and gateway handler do not install this middleware. This conditional exclusion depends on the unchanged fixed command/config surface, not on the package name alone. | A, B, C, N | Retain all three chi advisory IDs and re-evaluate any server/RC enablement. |
| [GO-2026-5856](https://pkg.go.dev/vuln/GO-2026-5856) | `stdlib` | Conditional: not enabled | **Input:** ECH-enabled child TLS handshake exposes PSK identity.. The child network route is plaintext loopback HTTP, with no TLS/ECH or proxy options supplied. Real Swift TLS is owned by the parent, whose analyzed toolchain is Go 1.27.1. No child ECH handshake is enabled in this contract. | A, B, N, S | Keep TLS termination in the parent; any future direct child TLS route requires separate ECH/session-resumption analysis. |
| [GO-2026-5942](https://pkg.go.dev/vuln/GO-2026-5942) | `golang.org/x/net`, `stdlib` | Conditional: not enabled | **Input:** Malformed SVCB/HTTPS DNS resource parsed by affected resolver.. Child storage addresses are literal loopback IPv4 and no proxy host is inherited. Upstream DNS resolution occurs in the parent transport. This excludes the inspected child network lookup input; native filesystem/UNC DNS behavior is not a claim about this Go resolver. | A, B, N, S | Preserve literal-host and no-proxy invariants, including startup/auth retry; reopen if child hostnames become configurable. |
| [GO-2026-6061](https://pkg.go.dev/vuln/GO-2026-6061) | `google.golang.org/grpc` | Conditional: not enabled | **Input:** xDS RBAC policy or client-initiated HTTP/2 stream reset against grpc-go server.. The primary advisory describes server RBAC and server HTTP/2 conditions despite broad client symbols in the scan. Swift uses ncw/swift HTTP, not gRPC; no child server command is enabled and the parent gateway is net/http with upstream HTTP/2 disabled. | A, B, C, N, S | Keep server-role and transport separation explicit; do not treat every reported gRPC client frame as an independently demonstrated client defect. |
| [GO-2026-6089](https://pkg.go.dev/vuln/GO-2026-6089) | `stdlib` | Conditional: not enabled | **Input:** New connection to server configured for unencrypted HTTP/2.. Child server commands/RC/metrics are not enabled. The parent gateway has its own net/http server, is built under the analyzed Go 1.27.1 toolchain, and does not configure h2c. The affected child server prerequisite is absent from the inspected route. | A, B, C, N | Preserve listener/default-protocol controls; actual application artifacts still need their own toolchain evidence. |
| [GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090) | `stdlib` | Conditional: not enabled | **Input:** Malicious client floods TLS server with state-advancing post-handshake messages.. The advisory describes a server attacked by a client. The rclone child neither serves TLS nor uses TLS on its loopback Swift route. Parent upstream TLS is a separate client/toolchain. TLS symbol presence alone is not this server exposure. | A, B, C, N, S | Reassess if any child TLS listener is introduced; retain parent/child role distinction in candidate review. |
| [GO-2026-6091](https://pkg.go.dev/vuln/GO-2026-6091) | `stdlib` | Conditional: not enabled | **Input:** Attacker value rendered in a JavaScript regexp context by html/template.. Child server and OAuth HTML flows are not selected by the fixed Swift/local commands and supplied token/URL. Object HTML bytes are copied, not rendered by Go html/template. Frontend/WebView rendering is a separate assessment. | A, B, C, D, S, T | Keep server/OAuth exclusion and opaque-byte controls; do not use this row as frontend XSS clearance. |
| [GO-2026-6181](https://pkg.go.dev/vuln/GO-2026-6181) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** RC job failure returned through remote-control API.. RC start is gated by an option that defaults false and cannot be supplied by the fixed flags/environment. The parent gateway is not rclone RC. Ordinary child stderr failures are a different diagnostics surface. | A, B, C, N | Preserve no-RC listener controls and continue separate child-log privacy tests. |
| [GO-2026-6182](https://pkg.go.dev/vuln/GO-2026-6182) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Attacker filename interpolated into a remote SFTP PowerShell command.. The affected quoting helper is called by SFTP, whose constructor is not selected by fixed Swift or absolute local specs. Windows local transfers use native file operations, and the parent starts the child with exec.Command, not a shell. | A, B, Q, Y | Keep backend-spec and argv controls; do not equate this exclusion with all Windows filename safety. |
| [GO-2026-6183](https://pkg.go.dev/vuln/GO-2026-6183) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** WebDAV Infinite Scale TUS transport failure yields nil response.. The TUS helper belongs to WebDAV Fs. Inspected Swift construction selects ncw/swift and its ordinary request/upload API, so the TUS response path is not enabled by these fixed remote types. | A, B, S, W | Preserve factory selection; retain independent Swift transport-error/cancellation behavior in the compatibility gate. |
| [GO-2026-6184](https://pkg.go.dev/vuln/GO-2026-6184) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Traversal request to rclone serve restic.. The affected middleware is installed by the separate restic server command, which is not an allowed invocation. The application gateway has a different handler/path policy and does not reuse that middleware. | A, B, C, N | Keep serve/RC disabled and test gateway confinement separately; this does not clear filesystem root authority. |
| [GO-2026-6185](https://pkg.go.dev/vuln/GO-2026-6185) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Crafted member paths processed by archive extract.. ArchiveExtract is a distinct command function. copy/copyto pass object bytes to selected backends without archive extraction; an archive object name or content type does not itself select this command. | A, B, D, X | Retain fixed-command controls and opaque archive-byte compatibility cases; reassess future extraction features. |
| [GO-2026-6186](https://pkg.go.dev/vuln/GO-2026-6186) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Remote names decoded under local encoding omitting Dot or Windows BackSlash.. Local conversion is enabled and names are untrusted. The required nondefault encoder is not exposed: default OS masks include Dot and Windows BackSlash, with no flag/config/env override. This excludes the documented encoding prerequisite only; pre-existing symlinks and concurrent filesystem changes remain P18 concerns. | A, B, D, L, Y | Verify effective masks/flags on each candidate and platform; retain original-name/collateral checks and the separate P18 hold. |
| [GO-2026-6187](https://pkg.go.dev/vuln/GO-2026-6187) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Crafted name reaches FTP commands under custom encoding.. FTP construction and custom FTP encoding are not selected by fixed Swift/local specs. This conclusion follows the actual backend selector and option boundary; it is not based just on the advisory title. | A, B, F, S | Preserve selector/encoding exclusion and re-evaluate any future FTP or generic remote surface. |
| [GO-2026-6188](https://pkg.go.dev/vuln/GO-2026-6188) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Sensitive S3 headers survive backend redirect handling.. The S3 constructor builds an AWS client that the Swift constructor does not use. Shared ordinary HTTP transport does not by itself install S3 header logic. Independently, the gateway refuses upstream redirects before the Swift child receives them. | A, B, N, O, S | Retain Swift real-child redirect tests; do not use an S3 fix or this exclusion to assert generic Swift redirect safety. |
| [GO-2026-6189](https://pkg.go.dev/vuln/GO-2026-6189) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Crafted object path sent to rclone serve s3.. The S3 server is a separate command, not an enabled backend or gateway handler. It is not started by fixed copy/copyto invocations with RC/metrics defaults. | A, B, C, N | Maintain no-server controls and independently test app gateway path/authority behavior. |
| [GO-2026-6190](https://pkg.go.dev/vuln/GO-2026-6190) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Untrusted mode/uid metadata restored to local destination.. Local writes are enabled, but metadata preservation defaults false and the wrapper supplies no metadata option/config/env. GetMetadataOptions returns before reading source metadata when disabled. This does not claim default file mode, native metadata or cleanup behavior is safe. | A, B, D, M | Retain effective metadata-off controls and per-platform output permission witnesses; do not enable metadata to improve fidelity without review. |
| [GO-2026-6191](https://pkg.go.dev/vuln/GO-2026-6191) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Untrusted translated .rclonelink target created under --links.. Link translation and copy-links both default false and cannot be added through the fixed wrapper surface. That removes the described translated-link input path. Ordinary path-based writes can still follow existing descendant links, so this is not a P18 symlink-confinement disposition. | A, B, D, Y | Preserve effective links-off controls and opaque .rclonelink file behavior; retain distinct existing-link/rename/cleanup negative controls. |
| [GO-2026-6195](https://pkg.go.dev/vuln/GO-2026-6195) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Restic server private-folder authorization middleware.. No restic server command is invoked. Its authenticated-user/folder middleware is separate from the parent storage gateway. Keep this authorization advisory distinct from the restic path-traversal ID. | A, B, C, N | Keep serving unavailable and re-review if any rclone server role becomes supported. |
| [GO-2026-6196](https://pkg.go.dev/vuln/GO-2026-6196) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** S3 session token on HTTPS-to-HTTP redirect.. No S3 remote/client or S3 session token is configured; Swift has a different client and token field. The parent gateway separately refuses redirects and retains the real Swift token. This is a conditional S3-path exclusion, not a general credential-leak guarantee. | A, B, N, O, S | Preserve backend selection and all real-child Swift redirect/token controls on the candidate. |
| [GO-2026-6197](https://pkg.go.dev/vuln/GO-2026-6197) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** WebDAV authorization across downgrade redirect, including shared REST callback.. The shared PreserveMethodRedirectFn is installed by inspected WebDAV code. Swift uses ncw/swift instead of this REST callback, and the gateway suppresses redirects. Exclusion is based on the caller/transport distinction, not merely the WebDAV label. | A, B, N, S, W | Inventory any future REST helper caller before reusing it; retain independent Swift downgrade/same-host/cross-host tests. |
| [GO-2026-6199](https://pkg.go.dev/vuln/GO-2026-6199) | `github.com/rclone/rclone` | Conditional: not enabled | **Input:** Unbounded response from configured HTTP CONNECT proxy.. The shared helper is called by inspected FTP/SFTP proxy paths. Swift uses ordinary fshttp transport; fixed environment omits proxy variables and no proxy flag is supplied. Its literal loopback route does not request CONNECT, while parent upstream transport has no proxy configured. | A, B, N, S, F, Q, P | Preserve no-proxy/no-other-backend controls, including startup and retries; reassess proxy support before enabling it. |
| [GO-2026-6303](https://pkg.go.dev/vuln/GO-2026-6303) | `golang.org/x/crypto` | Conditional: not enabled | **Input:** SSH server accepts permissions from non-public-key authentication callback.. This requires an SSH server authentication callback. Fixed commands do not serve SSH; the selected Swift/local byte routes do not construct an SSH server. SFTP client code, although linked, is also not the selected route. | A, B, C, Q, S | Keep server/backend selection constraints; do not infer protection for any future SSH role. |
| [GO-2026-6354](https://pkg.go.dev/vuln/GO-2026-6354) | `golang.org/x/crypto` | Conditional: not enabled | **Input:** Malicious SSH peer deadlocks an undecided channel.. This advisory applies to SSH clients as well as servers, so server absence alone is insufficient. Inspected SFTP code is the SSH-client constructor; fixed Swift/local specs select neither SFTP nor an SSH server. Local UNC is a native file route, not SSH. | A, B, C, Q, S, Y | Retain both client-backend and server-role exclusions; candidate SSH library fixes remain desirable for inventory maintenance. |
| [GO-2026-6355](https://pkg.go.dev/vuln/GO-2026-6355) | `golang.org/x/crypto` | Conditional: not enabled | **Input:** Malicious SSH peer deadlocks an established channel.. The same client-and-server prerequisite applies as 6354. No SSH transport is selected by the fixed route contract; shared symbol presence is retained rather than collapsed into the preceding ID. | A, B, C, Q, S, Y | Keep this separate protocol-state condition in candidate inventory and re-evaluate any SSH/SFTP support. |

## Highest-priority unresolved paths

GO-2026-6218, relative URL resolution: the old HTTP client resolves a redirect Location through the affected method before the redirect callback, the gateway refuses upstream 3xx responses and strips Location, and the child Swift dependency assembles paths without the affected method, so the inspected ordinary redirect path is cut off; shared REST joining and other linked callers still need a complete input and caller disposition, so the row stays unknown.
GO-2026-5970, Unicode normalization: copy traversal installs an NFC transformation, local upload paths and recorded download items require UTF-8, and the gateway path check occurs after the child constructs its request; cross-account copy names, backend encoding transformations and malformed-response conversions still need tracing to the affected iterator, so the row stays unknown.
The other ten unknown rows need specific Root, image-decoder, baggage, compression-dictionary, deprecated OpenPGP, ASN.1 or XML caller and input mapping; unknown means this bounded inspection supplies no complete answer and is neither an exposure assertion nor an exclusion.

## Conditions behind the proposed exclusions

The wrapper constructs version, copy and copyto commands, closes flags before paths, creates an empty configuration and supplies only its named Swift fields with PATH and a private HOME; application local paths are absolute and remote types are fixed to Swift.
The engine's factory selects the local backend for local paths, and the inspected SMB, FTP, SFTP, S3 and WebDAV constructors are distinct from the Swift constructor; remote control and metrics default off and their startup functions check those settings before listening; the parent gateway uses its own handler, literal loopback HTTP for the child, a direct captured upstream transport and parent-owned TLS.
Local encoding, metadata and translated-link advisories remain conditional because local writes are enabled: the operating-system encoding masks include the full-stop class and, on Windows, the backslash class with no override, metadata and link translation default off, and these are narrow prerequisite exclusions that do not address the separate root-authority contract.
The SSH deadlock advisories describe client as well as server paths, so their rows require exclusion of the SFTP client constructor as well as server commands; the WebDAV redirect advisory names a shared REST callback and its row cites the actual callback installation and the separate Swift transport; the CONNECT advisory names a shared proxy helper and its row cites its FTP and SFTP callers, the absent proxy settings and the child's literal loopback destination.

## Source citations

Each bundle names the exact retained source line inspected, with the first twelve hex digits of the retained file digest; paths are relative to the frozen application worktree (app), the public engine source (rclone), the Swift dependency source (swift) or the Go standard library source (go), and the structured record keeps the full digest, the line text and the public reference, which is not a reproducible-build proof for the shipped binary.

### Bundle A

- `app/internal/transfer/rclone/runner.go:321` (ad87bfb187a3): Copy and CopyTo commands are constructed, with a final -- separator.
- `app/internal/transfer/rclone/runner.go:360` (ad87bfb187a3): The second transfer command is copyto.
- `app/internal/transfer/rclone/runner.go:186` (ad87bfb187a3): The version probe passes version without --check.
- `rclone/cmd/version/version.go:25` (fe3aa2737361): Online version checking defaults false.
- `app/internal/transfer/rclone/runner.go:439` (ad87bfb187a3): Child is exec.Command with an explicit argv, not a shell.

### Bundle B

- `app/internal/transfer/rclone/runner.go:370` (ad87bfb187a3): Pacing options are the only appended configurable flags.
- `app/internal/transfer/rclone/runner.go:403` (ad87bfb187a3): Empty config; PATH and private HOME plus fixed Swift remote fields only.
- `app/internal/transfer/rclone/gateway.go:85` (61a51ba96efc): Remote names must be the two fixed names.
- `rclone/fs/newfs.go:164` (dbdb53257366): Backend is selected from the remote type; paths without a remote name select local.
- `app/internal/transfer/local/local.go:66` (03da5aff91a7): Local upload input is an absolute clean UTF-8 path.
- `app/internal/transfer/local/local.go:155` (03da5aff91a7): Selected download root is an absolute clean UTF-8 path.

### Bundle C

- `rclone/fs/rc/rc.go:20` (eca952eb8681): Remote-control option defaults false.
- `rclone/cmd/cmd.go:424` (b559a7b0a136): Global startup delegates to guarded RC and metrics starts.
- `rclone/fs/rc/rcserver/rcserver.go:38` (cf0cd25d3005): No RC listener starts unless Enabled is true.
- `rclone/fs/rc/rcserver/metrics.go:38` (521984c475ac): Metrics listener requires configured addresses.
- `rclone/cmd/serve/restic/restic.go:192` (51f13038acf0): Restic starts from its own selected command.
- `rclone/cmd/serve/s3/s3.go:106` (b8f5dec628b1): S3 server is a separate command.
- `rclone/fs/rc/rc.go:94` (eca952eb8681): Metrics listen addresses default to an empty list.

### Bundle D

- `app/internal/app/localtransferservice.go:263` (14333b2deb40): Upload uses fixed destination remote spec.
- `app/internal/app/localtransferservice.go:339` (14333b2deb40): Download uses fixed source remote spec and selected local directory.
- `app/internal/transfer/copy/executor.go:321` (39ca0fc9b6f9): Cross-account Copy passes fixed remote specs into the same runner.
- `app/internal/transfer/local/download_execute.go:152` (f5e84024a44b): Engine download writes to generated local stage before native placement.
- `rclone/fs/operations/copy.go:213` (57e4a678282a): Manual copy passes the source byte reader to the selected destination backend.
- `rclone/fs/operations/copy.go:244` (57e4a678282a): Manual copy opens object bytes; this path does not select an image or archive decoder.
- `rclone/fs/mimetype.go:47` (ac8207736146): MIME fallback uses name extension, not image decoding.

### Bundle F

- `rclone/backend/ftp/ftp.go:49` (61d86b683292): FTP has a distinct registered constructor.
- `rclone/backend/ftp/ftp.go:464` (61d86b683292): FTP invokes CONNECT helper only when its proxy is configured.

### Bundle H

- `go/src/net/http/client.go:650` (908d9d964cd1): Redirect handling reads Location then calls URL.Parse before CheckRedirect.
- `go/src/net/url/url.go:1098` (17db8d6dc576): URL.Parse resolves a reference, unlike the package Parse function.
- `go/src/net/url/url.go:1112` (17db8d6dc576): ResolveReference calls resolvePath.
- `rclone/lib/rest/url.go:9` (48c193960094): The shared REST URLJoin helper resolves a reference; inspected Swift constructor uses ncw/swift instead.

### Bundle I

- `rclone/backend/smb/smb.go:41` (59257ff71f27): SMB has a distinct registered constructor.
- `rclone/backend/smb/connpool.go:24` (62a4f2f42288): SMB Fs dials a network connection.
- `rclone/backend/smb/connpool.go:64` (62a4f2f42288): SMB connection uses cloudsoda DialConn; native local filesystem helper is separate.

### Bundle L

- `rclone/backend/local/local.go:758` (7ede36a80512): Local names pass through encoding then filepath.Join.
- `rclone/backend/local/local.go:327` (7ede36a80512): Default local encoding is encoder.OS.
- `rclone/lib/encoder/standard.go:16` (c2ad677dd084): Base includes EncodeDot.
- `rclone/lib/encoder/os_other.go:6` (b7ce6c47b479): Linux defaults to Base.
- `rclone/lib/encoder/os_darwin.go:8` (fc31921f690f): Darwin defaults include Base.
- `rclone/lib/encoder/os_windows.go:27` (75f62dadc543): Windows additionally encodes BackSlash.
- `app/internal/transfer/local/download_recording.go:94` (66195f642ced): Admitted download items reject dot components, backslashes and invalid UTF-8.

### Bundle M

- `rclone/fs/config.go:518` (b6df5c087943): Metadata preservation defaults false.
- `rclone/fs/metadata.go:155` (9867a576d905): GetMetadataOptions returns nil before source metadata access when disabled.
- `rclone/backend/local/local.go:1524` (7ede36a80512): Local Update requests metadata through that guard.

### Bundle N

- `app/internal/transfer/rclone/gateway.go:157` (61a51ba96efc): Parent dials the captured upstream directly and owns TLS.
- `app/internal/transfer/rclone/gateway.go:179` (61a51ba96efc): Gateway listener is literal IPv4 loopback.
- `app/internal/transfer/rclone/gateway.go:208` (61a51ba96efc): Only HTTP plus literal loopback host is sent to the child.
- `app/internal/transfer/rclone/gateway.go:294` (61a51ba96efc): Captured upstream replaces request authority; response is obtained with RoundTrip.
- `app/internal/transfer/rclone/gateway.go:311` (61a51ba96efc): Every upstream 3xx is refused before forwarding.
- `app/internal/transfer/rclone/gateway.go:368` (61a51ba96efc): Location and credential/hop headers are removed.
- `app/internal/transfer/rclone/gateway.go:191` (61a51ba96efc): Parent server handler is storageGateway, not a child server or chi router.

### Bundle O

- `rclone/backend/s3/s3.go:74` (cadc5296525b): S3 has a distinct registered constructor.
- `rclone/backend/s3/s3.go:1539` (cadc5296525b): S3 connection config selects AWS HTTP client.
- `rclone/backend/s3/s3.go:1815` (cadc5296525b): S3 NewFs constructs that client; Swift constructor does not use it.

### Bundle P

- `rclone/lib/proxy/http.go:23` (adc6d8aa8e24): CONNECT helper uses an optional supplied proxy; nil proxy directly dials.
- `rclone/lib/proxy/http.go:70` (adc6d8aa8e24): Configured CONNECT response uses unrestricted http.ReadResponse.

### Bundle Q

- `rclone/backend/sftp/sftp.go:57` (cb32d55407f6): SFTP has a distinct registered constructor.
- `rclone/backend/sftp/ssh_internal.go:25` (11eb972b1005): SFTP creates an SSH connection; optional CONNECT helper is local to that route.
- `rclone/backend/sftp/sftp.go:2064` (cb32d55407f6): PowerShell path quoting is in SFTP remote command construction.

### Bundle S

- `rclone/backend/swift/swift.go:474` (5ba35a36d4da): Swift uses ncw/swift with fshttp transport.
- `rclone/backend/swift/swift.go:500` (5ba35a36d4da): Environment authentication is conditional; supplied storage URL and token avoid fresh remote authentication.
- `rclone/backend/swift/auth.go:32` (02fc26fefac6): The auth wrapper with no parent creates no authentication request.
- `swift/swift.go:507` (0a744e73713e): The child Swift dependency owns a normal http.Client.
- `swift/swift.go:764` (0a744e73713e): Call uses package url.Parse and direct Path assembly.
- `swift/swift.go:1158` (0a744e73713e): Object listings explicitly request JSON and decode JSON.
- `rclone/fs/fshttp/http.go:211` (8d3238cf5497): Swift shared transport is a net/http transport, with proxy settings from config or env.
- `rclone/fs/fshttp/http.go:297` (8d3238cf5497): NewTransport uses this constructor.

### Bundle T

- `rclone/cmd/selfupdate/verify.go:12` (ca9a3b4e64c9): Self-update uses ProtonMail OpenPGP; this is not evidence about linked golang.org/x/crypto/openpgp callers.
- `rclone/lib/oauthutil/oauthutil.go:939` (db2d22191323): OAuth response renders a template; Swift token override has no OAuth setup.
- `rclone/lib/http/template.go:134` (afe0ae64f734): Template parsing belongs to server listing HTML.

### Bundle U

- `rclone/fs/march/march.go:69` (0e7e2623fed1): Copy traversal installs NFC comparison unless normalization is disabled.
- `rclone/fs/filter/filter.go:418` (89957a4639e3): files-from bypasses this filter normalization path, while hash filter can normalize.
- `rclone/backend/local/local.go:741` (7ede36a80512): Optional local normalization precedes invalid-UTF-8 warning.
- `app/internal/transfer/plan/plan.go:117` (7338ceefe48a): Name routing checks path syntax; it is not itself a UTF-8 validity check.
- `app/internal/transfer/local/download_recording.go:95` (66195f642ced): Recorded download names and paths require UTF-8.
- `app/internal/transfer/rclone/gateway_scope.go:41` (16b233164cac): Gateway request paths require UTF-8, checked after the child constructs them.

### Bundle W

- `rclone/backend/webdav/webdav.go:71` (8e08f0213abd): WebDAV has a distinct registered constructor.
- `rclone/backend/webdav/webdav.go:344` (8e08f0213abd): WebDAV request uses shared PreserveMethodRedirectFn.
- `rclone/lib/rest/rest.go:224` (cadeee399169): Shared callback changes redirect method; it is not a gateway policy.
- `rclone/backend/webdav/tus.go:45` (1f75aa1d5687): TUS response handling belongs to WebDAV Fs.

### Bundle X

- `rclone/cmd/archive/extract/extract.go:29` (155918ac2558): Archive extraction is its own selected command.
- `rclone/cmd/archive/extract/extract.go:95` (155918ac2558): ArchiveExtract is the advisory function, separate from Copy.

### Bundle Y

- `rclone/backend/local/local.go:95` (7ede36a80512): Follow links and translate links both default false.
- `rclone/backend/local/local.go:412` (7ede36a80512): TranslateSymlinks can be enabled by global links flag.
- `rclone/backend/local/local.go:1442` (7ede36a80512): Ordinary files use path-based OpenFile; translated links use a separate branch.
- `rclone/backend/local/local.go:1398` (7ede36a80512): Directory creation is also ordinary path-based file.MkdirAll.
- `rclone/lib/file/file_other.go:15` (38a094c64d58): Unix helper delegates to os.OpenFile.
- `rclone/lib/file/file_windows.go:21` (59892294574f): Windows helper uses native syscall file operations.

## What this record does not establish

No advisory was executed against a binary, no candidate engine was downloaded, built or scanned here, the twelve unknown rows are open, and the six-target compatibility gate, the npm scope, the native-library scope and the update policy remain the register's open SEC-11 items.
SEC-11, P31 and the valuable-data release hold remain blocking.

## Protected delivery

The delivered range is the nine logical commits between master 5a374826ddc2f69a and the reviewed head 121ce214abfe3421, cherry-picked patch for patch from three independently approved branches: this slice's three documentation commits (this dispositions record with its offline rescan, the native Linux journeys, and the cancel journey count correction), together with the three commits of the name length rule and the three commits of the deletion request authority correction.
Independent review approved each slice on its own base and then the combined range on its final base, with every patch marked identical by range-diff and no shared document in the range.

Its check run 34676247429 completed at the first attempt with all five required jobs successful: the frontend job in one minute and thirteen seconds, the headless smoke job in one minute and fifty-three seconds, the race-detector Go job in nine minutes and nineteen seconds, the containerized Swift integration job in twenty-four minutes and thirty-eight seconds, and the aggregate job.
The run retained the two coverage badges, the 481,236-byte scenario coverage artifact, the 6,114,561-byte smoke artifact and a 6,936-byte Swift diagnostics artifact.
The retained badges report combined Go statement coverage of 88.1 per cent and frontend coverage of 91.07 per cent.
On the same head the full race suite with the real Swift fixture reconciled to 3,820 leaves with 3,819 passing, none failing and the single expected matrix skip; no frontend file is in the range.

The pull request then left draft state after thirty-one premerge guards passed and was merged by rebase pinned to the reviewed head object at 06:07:38 UTC on 12 September 2026.
Master became ca95bad8a6226746, whose tree 70f19c19a077b799 is identical to the reviewed head's tree, and each of the nine commits kept its patch identity, message and author identity.
The resulting master check run 34677323044 concluded success at the first attempt with the same five required jobs.
The pipeline exercised Linux runners only, so native acceptance on the other platforms remains unproven, and the register text for the rows this record informs is in the same integration range as this record.
