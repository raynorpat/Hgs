# Credential revocation during Mercurial transfers

HTTPS password authentication is checked again for every new request. An active password-authenticated transfer is bound to the local password verifier, salt, and login identity from the same user row that passed authentication. After a local change, subsequent writer-hook checks reject the old credential version, and the service's periodic session cleanup detects the change. Key and token sessions keep their own credential identities; changing the account password does not itself revoke an independent key or token.

LDAP or SMTP provider-only password resets may not change any local user revision. Without a local change or provider event, an already authenticated transfer cannot detect that external reset. A new HTTPS authentication still checks the provider as before, and in-flight transfers remain bounded by `[mercurial] SESSION_TIMEOUT` (minutes; default 30). Local account disablement, key/token deletion, repository access loss, and ownership/privacy changes remain subject to the existing live checks. Do not interpret this as universal instantaneous revocation of external provider credentials.

The previously documented stream-bundle authorization limitation remains separate and unchanged; see [transport evidence](transport-evidence.md).
