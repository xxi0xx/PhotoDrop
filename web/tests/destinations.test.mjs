import test from 'node:test';
import assert from 'node:assert/strict';
import { linkResultMessage, targetReasonMessage } from '../src/lib/destinations.ts';

test('every link result code maps to one fixed message and unknown codes fail safe', () => {
  for (const code of ['linked', 'unlinked']) assert.equal(linkResultMessage(code).ok, true);
  for (const code of ['link_failed', 'link_wrong_user', 'link_subject_mismatch', 'link_in_use', 'link_version']) {
    const result = linkResultMessage(code);
    assert.equal(result.ok, false); assert.ok(result.text.length > 20);
  }
  assert.equal(linkResultMessage(null), null);
  // A crafted code never reflects into the page.
  const crafted = linkResultMessage('<script>alert(1)</script>');
  assert.equal(crafted.text, linkResultMessage('link_failed').text);
  assert.match(linkResultMessage('link_subject_mismatch').text, /LINK_MATCH=email/);
});

test('target reasons explain why linking is unavailable', () => {
  for (const reason of ['secrets_key', 'base_url', 'no_url', 'config_account', 'config_key_ignored', 'immich_version', 'immich_unavailable']) {
    assert.notEqual(targetReasonMessage(reason), targetReasonMessage(undefined));
  }
  assert.equal(targetReasonMessage('unknown'), targetReasonMessage(undefined));
});
