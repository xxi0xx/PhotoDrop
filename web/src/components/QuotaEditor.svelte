<script lang="ts">
  import { formatBytes, type EventRecord } from '../lib/api';
  import { quotaFields, quotaStatuses, type QuotaKey, type QuotaDraft } from '../lib/quotas';
  let { draft = $bindable(), event, fields, disabled }: { draft: QuotaDraft; event: EventRecord | null; fields: Record<string, string>; disabled: boolean } = $props();
  const groups: { title: string; kind: 'photos' | 'videos'; keys: QuotaKey[] }[] = [
    { title: 'Photo limits', kind: 'photos', keys: ['max_photos', 'max_photo_file_bytes', 'max_photo_storage_bytes'] },
    { title: 'Video limits', kind: 'videos', keys: ['max_videos', 'max_video_file_bytes', 'max_video_storage_bytes'] },
  ];
</script>
{#snippet input(key: QuotaKey)}
  <div class="field"><label for={key}>{quotaFields[key].label}</label><input id={key} name={key} type="number" min={quotaFields[key].unit === 1 ? 1 : 0.000000001} max={quotaFields[key].unit === 1 ? 1000000 : 1125899906842624 / quotaFields[key].unit} step={quotaFields[key].unit === 1 ? 1 : 'any'} bind:value={draft[key]} aria-invalid={!!fields[key]} aria-describedby={fields[key] ? `${key}-error` : 'quota-hint'} />{#if fields[key]}<p class="error" id={`${key}-error`}>{fields[key]}</p>{/if}</div>
{/snippet}
<p id="quota-hint" class="hint">Leave a limit blank for no limit at that scope. Completed and pending files count. The first applicable limit reached wins. The server also caps individual file size. 1 MiB = 1,048,576 bytes; 1 GiB = 1,073,741,824 bytes.</p>
{#if event}{#each quotaStatuses(event) as status}<p class="notice" role="status">{status}. Existing media are safe.</p>{/each}{/if}
<div class="quota-panels">
  {#each groups as group}
    <fieldset {disabled} class="quota-panel"><legend>{group.title}</legend>
      {#if event}{@const usage = event.media[group.kind]}<dl class="stat-grid"><div><dt>{group.kind === 'photos' ? 'Photos' : 'Videos'}</dt><dd>{usage.ready_count}{#if event[group.keys[0]] != null}<small> / {event[group.keys[0]]}</small>{/if}</dd></div><div><dt>Storage</dt><dd>{formatBytes(usage.bytes)}{#if event[group.keys[2]] != null}<small> / {formatBytes(event[group.keys[2]]!)}</small>{/if}</dd></div></dl><p class="hint">{usage.pending_count} pending, reserving {formatBytes(usage.reserved_bytes)}.</p>{/if}
      {#each group.keys as key}{@render input(key)}{/each}
    </fieldset>
  {/each}
</div>
<details><summary>Overall limits</summary><p class="hint">Optional safety ceilings across photos and videos together.</p><fieldset {disabled}>{@render input('max_assets')}{@render input('max_bytes')}</fieldset></details>
<p class="hint">Lowering limits never deletes media or cancels an existing typed reservation. New uploads must fit the new limits.</p>
<style>
  .quota-panels { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 17rem), 1fr)); gap: 1.5rem; }
  .quota-panel { min-width: 0; }
  legend { font-weight: 700; margin-bottom: 1rem; }
  summary { cursor: pointer; font-weight: 600; margin: 1rem 0; }
</style>
