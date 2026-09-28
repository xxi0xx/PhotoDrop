import { test } from 'node:test';
import assert from 'node:assert/strict';
import { loginError } from '../src/lib/auth.ts';
test('SSO failure is generic and never reflects provider query details', () => {
  assert.match(loginError('?error=oidc&error_description=private-token'), /SSO sign-in could not be completed/);
  assert.ok(!loginError('?error=oidc&error_description=private-token').includes('private-token'));
  assert.equal(loginError('?error=upstream-secret'), '');
  assert.equal(loginError(''), '');
});
