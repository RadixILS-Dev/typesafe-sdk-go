# Security Policy

## Reporting a vulnerability

Please report security issues privately through
[GitHub security advisories](https://github.com/RadixILS-Dev/typesafe-sdk-go/security/advisories/new)
rather than a public issue. We aim to acknowledge reports within five business
days.

## Handling credentials

- The API key comes from `WithAPIKey` or `TYPESAFE_API_KEY`. Never hard-code it
  in a repository, and keep `.env` files out of version control (ignored here).
- The SDK sends the key only as an `Authorization` header to the configured base
  URL. It never logs, and never returns, request bodies, headers, or credentials.
- `APIError.Endpoint` deliberately strips user info, query, and fragment
  components so credentials cannot leak through an error message or log line.
- Redirects are not followed by the default HTTP client, so the key is not
  forwarded to a redirect target. A client supplied with `WithHTTPClient` is
  used unchanged, including its redirect policy: if you enable redirects, pin
  the destinations you trust.
- Because the key is a bearer token, keep the default `https://` base URL in
  production and treat `TYPESAFE_BASE_URL` as trusted configuration.

## Scope

This policy covers this module only. Model behaviour, prompt injection in
application-supplied state or instructions, and how your application acts on
returned answers are application-level concerns. Treat model output as
untrusted input: validate a `Choice` against your own option list before using
it, and do not let answers drive privileged actions without your own checks.
