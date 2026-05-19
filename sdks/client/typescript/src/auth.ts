// SPDX-License-Identifier: MIT

/**
 * An Authenticator attaches an authentication credential to an
 * outbound request. The {@link Client} invokes apply on every request
 * before sending it. apply mutates the supplied Headers object;
 * returning a rejected promise aborts the request.
 */
export interface Authenticator {
  apply(headers: Headers): void | Promise<void>;
}

/**
 * bearerToken returns an {@link Authenticator} that sends token in the
 * Authorization header. Use it for an OIDC ID token, a Lenny-issued
 * access token, or a service-account token, per the §13 client
 * authentication model.
 */
export function bearerToken(token: string): Authenticator {
  return {
    apply(headers: Headers): void {
      headers.set('Authorization', `Bearer ${token}`);
    },
  };
}

/**
 * apiKey returns an {@link Authenticator} that sends key in the
 * X-Lenny-API-Key header.
 */
export function apiKey(key: string): Authenticator {
  return {
    apply(headers: Headers): void {
      headers.set('X-Lenny-API-Key', key);
    },
  };
}

/**
 * A TokenSource supplies a bearer token that may change over time. A
 * {@link refreshingToken} authenticator calls token before each
 * request, so an implementation can refresh a token before it
 * expires.
 */
export interface TokenSource {
  /**
   * Returns the current bearer token, refreshing it when necessary. A
   * rejected promise aborts the request.
   */
  token(): string | Promise<string>;
}

/**
 * refreshingToken returns an {@link Authenticator} that fetches the
 * bearer token from source before each request. It is the §15.6
 * token-refresh-before-expiry hook: a TokenSource implementation
 * refreshes the token when it is close to expiry and returns the
 * current value, and the SDK attaches whatever the source returns.
 */
export function refreshingToken(source: TokenSource): Authenticator {
  return {
    async apply(headers: Headers): Promise<void> {
      const tok = await source.token();
      headers.set('Authorization', `Bearer ${tok}`);
    },
  };
}
