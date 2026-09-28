export type AuthMethods = { password: boolean; oidc: boolean };
// Only this fixed local code is rendered; never echo provider/query content.
export function loginError(search: string): string {
  return new URLSearchParams(search).get('error') === 'oidc'
    ? 'SSO sign-in could not be completed. Please try again or contact your administrator.' : '';
}
