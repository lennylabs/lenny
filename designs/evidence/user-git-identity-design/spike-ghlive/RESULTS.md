# Live GitHub spikes (2026-11-06)

Owed by `designs/user-git-identity-design.md` "Validation owed": S1, S2, S3, S5, S6, S15, and S16. These are the first runs against a real provider. Earlier runs in `../spike-gh/` used a recording stub.

## Environment

- Organization `lenny-test` on the GitHub free plan, with public repositories `test-repo` and `test-repo-2`. `test-repo` has a ruleset on `main` that requires a pull request with 1 approval, approval of the most recent reviewable push (`require_last_push_approval`), no stale-review dismissal, and `require_extra_approval_for_unattributed_changes`. `test-repo-2` has no rules. The base permission is Write.
- GitHub App `lenny-spike-test-app`, owned by the organization and installed on all repositories, with contents write, pull requests write, metadata read, and email addresses read. User token expiry and device flow are on, and the webhook is inactive.
- Two member accounts, `lennylabsdev` (Alice in the scripts) and `itsfivepastnine` (Bob), each authorized through device flow.
- Every call ran from an `alpine:3.22` container with curl, jq, openssl, and git 2.49.1. Tokens were kept on a tmpfs in that container, and no output contains a token. The credentials were mounted read-only from a file outside the repository.
- Afterwards every spike pull request was closed, every `spike/` branch was deleted, and every user token and child was revoked (`raw/cleanup.txt`).

"Child" means a token from `POST /applications/{client_id}/token/scoped`. "Parent" means the user's App token.

## Results

| Spike | Result | Raw |
|:--|:--|:--|
| S1 | A child keeps working after its parent is refreshed. The old parent answers 401 after the refresh, and the new parent answers 200. A child's `expires_at` is 8 hours from its own creation, independent of the parent's remaining life. Refresh returns a new refresh token each time, and three refreshes in a row succeeded. | `s1s5.txt`, `s2.txt` |
| S2 | No per-user token cap was found for children. One parent issued 50 children with the same repository and permissions within one hour, after about 6 others that hour, and all 50 stayed valid, with no revocation of older ones and no re-authorization prompt. The scoped endpoint counted against the `core` rate limit (`x-ratelimit-remaining` fell by 1 per call). GitHub's 2,000-per-hour limit on token requests per App was not reached, so its scope is unverified. | `s2.txt`, `s2b.txt` |
| S3 | On `test-repo`, after Bob opened a pull request: a push by the App's installation token, with Alice as commit author, followed by Alice's approval gave `APPROVED` and `CLEAN`, so GitHub did not treat Alice as the pusher. The same with an author that is no GitHub account gave the same result, so `require_extra_approval_for_unattributed_changes` did not apply. A push by Alice's own user token, and a push by a child of it, each followed by her approval, gave `REVIEW_REQUIRED` and `BLOCKED`, so GitHub treated Alice as the pusher in both. A scoped child can push. The author's own approval is refused with 422 "Can not approve your own pull request". | `s3.txt`, `s3b_s16.txt` |
| S5 | Revoking one child (204) leaves the parent and a sibling child valid. | `s1s5.txt` |
| S6 | The `permissions` object on `GET /repos/{owner}/{repo}` reports the user's role from the organization's base permission (`push: true`) for both the parent and a child limited to `metadata: read`, so it does not reflect the child's narrowing. `GET /repos/{owner}/{repo}/collaborators/{user}/permission` answers 200 with `write` for the parent and for a metadata-only child. `GET /user/emails` answers 200 with the verified primary email for the parent, for a child with only `contents: read`, and for a child with `emails: read`, so the scoped endpoint does not narrow account permissions. Whether one verified email can sit on two accounts was not tested. | `s6s15.txt` |
| S15 | A child with `pull_requests: write` and `contents: read` approved a pull request (200, review `APPROVED`). The same child could not merge through REST (403) or GraphQL `mergePullRequest` ("Resource not accessible by integration"). A child limited to `test-repo` could not act on `test-repo-2` through a GraphQL node-ID mutation (`addComment`, refused) or REST (403). GraphQL refused an array batch (400 "Body should be a JSON object"), and `GET /graphql` with a `query` parameter returned the schema rather than running the query. A form-encoded body to a REST write was refused ("Problems parsing JSON"). `X-HTTP-Method-Override: DELETE` and `?_method=DELETE` on a POST were ignored (422, ref intact). A pull request body of 4,194,304 characters was accepted, so no limit was found at or below 4 MiB. A comment made with a child carries `performed_via_github_app` naming the App. A scoped request with an empty repository list is refused ("1 item required"). `gh pr view` under a narrow child and workflow triggering were not run. | `s6s15.txt`, `s3b_s16.txt`, `scopecheck.txt` |
| S16 | An approval survives a change of the pull request's base to an unprotected branch, on both repositories. Changing the base back to `main` on `test-repo`, whose ruleset requires review, dismissed the approval (`DISMISSED`, `REVIEW_REQUIRED`). | `s3b_s16.txt`, `s16b.txt` |

Not run: S4 (SAML), S11 to S13 and S17 (GitLab), and S14 (GitHub Enterprise Server).
