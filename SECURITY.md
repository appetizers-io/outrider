# Security

Report vulnerabilities privately through [GitHub security advisories](https://github.com/appetizers-io/outrider/security/advisories/new).
Include the affected version, operating system, configuration, a minimal
reproduction and the observed impact. Remove credentials, private code and
personal data from reports. Do not publish a working exploit before a fix
can be coordinated. No response-time guarantee is offered.

## Threat model

Outrider reads GitHub PRs and starts your locally authenticated coding agent.
PR descriptions, discussions, repository instructions, code and linked issues
are untrusted. They can contain prompt injection or executable malicious code.
A review that runs tests executes the contributor's code as your user unless
an OS sandbox or microVM contains it.

PATH guards are workflow controls, not an isolation boundary. They load policy
from beside their per-session executable copies, ignore agent-supplied policy
variables, restrict GitHub writes and enforce review-fork destinations. An
unsandboxed agent with arbitrary code execution can call real tools directly,
read your credentials or modify metadata using other tools. Native deny rules
and classifiers do not make that impossible.

Use `sandbox: read-only` for static untrusted reviews. It blocks file writes
and network access at the OS boundary, but does not hide every readable host
file from the agent process. Docker Sandboxes isolate the host filesystem and
keep changes in a private clone; copied credentials and forwarded SSH identities
still grant remote capabilities. See [isolation limits](docs/isolation.md).

The launch classifier receives PR text, activity and failed-check information.
The coding agent sends the prompt, discussion and code it reads to its model
provider. Review the providers' terms before using private repositories.

See [the detailed safety model](docs/safety.md) for permissions, approval
behaviour, sandbox requirements, data flows and remaining limitations. Versions
before a published fix may retain known gaps; use the latest release and check
the release notes. There is no maintained security backport branch.
