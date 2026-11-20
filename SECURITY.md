# Security

Yawl runs your apps, holds their secrets and has root on your servers, so we take reports seriously.

## Reporting a vulnerability

Please do not open a public issue. Report it privately through GitHub: the repository's **Security** tab, **Report a vulnerability**.

Include what an attacker needs (a role in the panel, network access, nothing), the steps, and what they get. A proof of concept helps.

You will get an answer within 7 days. A fix ships in a signed release, and the advisory names you unless you ask otherwise.

## Supported versions

Only the latest release gets fixes. `yawl update` moves the panel and its agents to it.

## Releases

Every release's `SHA256SUMS` is signed with an Ed25519 key. The installer checks it against the key written into the installer, and `yawl update` against the key built into the binary you already run; a mismatch stops both.
