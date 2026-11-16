<!-- The application settings (§38/§40) in one place, grouped by what each
  governs, with backup and restore (ADR 0098). Every control shows the last
  observed value until a save is acknowledged, and a refusal beside itself. -->
<script lang="ts">
  import { onMount } from "svelte";
  import { trapFocus } from "../../lib/trapFocus";
  import type { AppSettingsDTO } from "../../lib/api";
  import type { SettingsState } from "../browser/settingsState.svelte";

  let {
    settings,
    onClose,
    onImportBookmarks = () => {},
  }: { settings: SettingsState; onClose: () => void; onImportBookmarks?: () => void } = $props();
  const id = $props.id();

  onMount(() => {
    // Reopening the dialog is not the explicit recovery read after a lost
    // reply, whichever setting that save was for.
    if (settings.status !== "unconfirmed") void settings.load();
  });

  async function saveBound(patch: Partial<AppSettingsDTO>, report: (message: string | null) => void) {
    if (!settings.canSave || !settings.document) return;
    report(null);
    try {
      await settings.save({ ...settings.document, ...patch });
    } catch (err) {
      report(err instanceof Error ? err.message : String(err));
    }
  }

  // The profiles the service accepts (§40); transfer dialogs start from the
  // one chosen here.
  const PROFILE_CHOICES = [
    { value: "auto", label: "Auto" },
    { value: "balanced", label: "Balanced" },
    { value: "aggressive", label: "Aggressive" },
    { value: "custom", label: "Custom" },
  ];
  let profileError: string | null = $state(null);

  // The budgets the service accepts (ADR 0105): one it would refuse is not
  // offered.
  const BUDGET_CHOICES = [1, 2, 3, 4];
  // While a budget save is unanswered, or its outcome is unknown and no read
  // has followed, the control shows the budget that save asked for;
  // otherwise what was last read. Other writes hold the settings too, a rail
  // resize or a restore, and say nothing about the budget.
  const budgetAsked = $derived.by(() => {
    const attempt = settings.attempt;
    const unknown = attempt?.state === "unconfirmed" && settings.status === "unconfirmed";
    return attempt?.state === "pending" || unknown ? attempt?.value : undefined;
  });
  const budgetValue = $derived(String(budgetAsked ?? settings.document?.maxActiveJobs ?? ""));

  // The numeric settings share one treatment: the service refuses a value
  // outside the range rather than clamping it, so each control shows the last
  // observed value until a save is acknowledged, and a refusal beside itself.
  type BoundedSetting = {
    field:
      | "downloadObjectCeiling"
      | "browserRetentionCeiling"
      | "maxQueuedJobs"
      | "maxConnsPerHost"
      | "jobRetentionDays"
      | "auditRetentionDays";
    /** Names the control's note and status elements. */
    slug: string;
    label: string;
    /** What the setting governs, and the range the service accepts. */
    note: string;
    min: number;
    max: number;
    step: number;
    /** What the observed value means, in the operator's terms. */
    says: (value: string) => string;
  };
  const days = (value: string) => `${value} ${value === "1" ? "day" : "days"}`;
  const JOB_LIMITS: BoundedSetting[] = [
    {
      // ADR 0074 decision 2.
      field: "maxQueuedJobs", slug: "queue-depth", label: "Queue depth",
      note: "The most jobs that may wait behind the active slots; a further start is refused with the count named (1 to 1,000).",
      min: 1, max: 1000, step: 1,
      says: (value) => `${value} waiting at most`,
    },
    {
      // ADR 0072.
      field: "downloadObjectCeiling", slug: "ceiling", label: "Download ceiling",
      note: "The most objects one download selection may hold; a wider prefix is refused rather than cut short (1,000 to 5,000,000).",
      min: 1000, max: 5_000_000, step: 1000,
      says: (value) => `ceiling ${value} objects`,
    },
  ];
  const NETWORK_LIMITS: BoundedSetting[] = [
    {
      // ADR 0074 decision 1.
      field: "browserRetentionCeiling", slug: "retention", label: "Browser retention",
      note: "The most rows a browser column keeps loaded; at the ceiling the column keeps what it shows and loads no further page (1,000 to 5,000,000).",
      min: 1000, max: 5_000_000, step: 1000,
      says: (value) => `keeps ${value} rows`,
    },
    {
      // ADR 0074 decision 4: the native client's cap applies to sessions
      // opened after the save.
      field: "maxConnsPerHost", slug: "connection-cap", label: "Connection cap",
      note: "The most connections one session opens to a host; a burst of reads waits for a connection rather than opening one without bound. It applies to sessions opened after the save (1 to 256).",
      min: 1, max: 256, step: 1,
      says: (value) => `${value} connections per host on the next connect`,
    },
  ];
  // §51: the prune runs once, at startup, so a change takes effect on the
  // next start; 0 switches that prune off.
  const RECORD_LIMITS: BoundedSetting[] = [
    {
      field: "jobRetentionDays", slug: "job-retention", label: "Job history kept (days)",
      note: "Finished jobs older than this leave the job history the next time the app starts; a job still holding recovery records is kept. 0 keeps every job (0 to 3,650).",
      min: 0, max: 3650, step: 1,
      says: (value) => (value === "0" ? "every finished job is kept" : `finished jobs kept ${days(value)}`),
    },
    {
      field: "auditRetentionDays", slug: "audit-retention", label: "Audit trail kept (days)",
      note: "Audit events older than this are removed the next time the app starts. 0 keeps every event (0 to 3,650).",
      min: 0, max: 3650, step: 1,
      says: (value) => (value === "0" ? "every event is kept" : `events kept ${days(value)}`),
    },
  ];
  let boundedErrors: Partial<Record<BoundedSetting["field"], string | null>> = $state({});

  // Whether the transfer engine may remove a replaced large object's old
  // segments as it writes the new object (ADR 0122 decision 3). Defaults to
  // removing them, which is what rclone's own replacement does; the control
  // is for the operator who shares segments between manifests and cannot
  // accept it.
  const reclaimChecked = $derived(settings.document?.reclaimReplacedFragments ?? true);
  let reclaimError: string | null = $state(null);
</script>

{#snippet bounded(setting: BoundedSetting, observed: number)}
  <div class="setting">
    <label class="field">
      <span>{setting.label}</span>
      <input
        class="number num"
        type="number"
        min={setting.min}
        max={setting.max}
        step={setting.step}
        value={String(observed)}
        disabled={!settings.canSave}
        aria-describedby={`${id}-${setting.slug}-note ${id}-${setting.slug}-status`}
        onchange={(e) => {
          const typed = e.currentTarget.value;
          const value = Number(typed);
          e.currentTarget.value = String(observed);
          // The service takes whole numbers. Anything else would fail the
          // bridge's decoding, which the window cannot tell from a lost
          // reply, so it is not sent. An emptied field is no number either;
          // read as 0 it would switch a retention prune off.
          if (typed.trim() === "" || !Number.isInteger(value)) {
            boundedErrors[setting.field] = `${setting.label} takes a whole number`;
            return;
          }
          void saveBound({ [setting.field]: value }, (message) => (boundedErrors[setting.field] = message));
        }}
      />
    </label>
    <p class="note" id={`${id}-${setting.slug}-note`}>{setting.note}</p>
    <p class="status" id={`${id}-${setting.slug}-status`} role="status">
      {boundedErrors[setting.field] || setting.says(observed.toLocaleString("en-US"))}
    </p>
  </div>
{/snippet}

<div class="scrim" role="presentation" onclick={(e) => e.target === e.currentTarget && onClose()}>
  <div class="dialog" role="dialog" aria-modal="true" aria-labelledby={`${id}-title`} tabindex="-1" use:trapFocus={onClose}>
    <header>
      <h2 id={`${id}-title`}>Settings</h2>
      <button class="close" onclick={onClose} aria-label="Close settings">✕</button>
    </header>

    <div class="body">
      {#if settings.document?.fileProblem}
        <!-- The defaults are in force until a save, which keeps the bad file aside (ADR 0109). -->
        <p class="note error" role="alert">{settings.document.fileProblem}</p>
      {/if}
      {#if settings.document}
        {@const current = settings.document}
        <section aria-labelledby={`${id}-general`}>
          <h3 id={`${id}-general`}>General</h3>
          <div class="setting">
            <label class="field">
              <span>Default performance profile</span>
              <select
                value={current.defaultProfile}
                disabled={!settings.canSave}
                aria-describedby={`${id}-profile-note ${id}-profile-status`}
                onchange={(e) => {
                  const value = e.currentTarget.value;
                  e.currentTarget.value = current.defaultProfile;
                  void saveBound({ defaultProfile: value }, (message) => (profileError = message));
                }}
              >
                {#each PROFILE_CHOICES as profile (profile.value)}
                  <option value={profile.value}>{profile.label}</option>
                {/each}
              </select>
            </label>
            <p class="note" id={`${id}-profile-note`}>
              Copy, upload and download dialogs start from this profile; each job can still choose another in its
              per-job settings.
            </p>
            <p class="status" id={`${id}-profile-status`} role="status">{#if profileError}{profileError}{/if}</p>
          </div>
        </section>

        <section aria-labelledby={`${id}-jobs`}>
          <h3 id={`${id}-jobs`}>Jobs</h3>
          <div class="setting">
            <label class="field">
              <span>Active-job budget</span>
              <select
                value={budgetValue}
                disabled={!settings.canSave}
                aria-describedby={`${id}-budget-note ${id}-budget-status`}
                onchange={(e) => {
                  const value = Number(e.currentTarget.value);
                  e.currentTarget.value = budgetValue;
                  // The outcome is stated in the budget status at the foot,
                  // which the settings state keeps across a close; nothing is
                  // reported here.
                  void saveBound({ maxActiveJobs: value }, () => {});
                }}
              >
                {#each BUDGET_CHOICES as n (n)}
                  <option value={String(n)}>{n}</option>
                {/each}
              </select>
            </label>
            <p class="note" id={`${id}-budget-note`}>
              How many jobs run at once (1 to 4); a further start waits in the queue,
              and each job brings its own engine settings, so a profile's transfers and limits apply per job, not in total.
            </p>
          </div>
          {#each JOB_LIMITS as setting (setting.field)}
            {@render bounded(setting, current[setting.field])}
          {/each}
          <div class="setting">
            <label class="field">
              <span>Reclaim replaced segments</span>
              <input
                class="reclaim-fragments"
                type="checkbox"
                checked={reclaimChecked}
                disabled={!settings.canSave}
                aria-describedby={`${id}-reclaim-note ${id}-reclaim-status`}
                onchange={(e) => {
                  const value = e.currentTarget.checked;
                  e.currentTarget.checked = reclaimChecked;
                  void saveBound({ reclaimReplacedFragments: value }, (message) => (reclaimError = message));
                }}
              />
            </label>
            <p class="note" id={`${id}-reclaim-note`}>
              When an upload replaces a large object, the transfer engine removes the old object's segments from the
              destination's segments container as it writes the new one; off keeps them, and a container that keeps
              versions keeps them either way.
            </p>
            <p class="status" id={`${id}-reclaim-status`} role="status">
              {#if reclaimError}{reclaimError}{:else}{current.reclaimReplacedFragments ? "a replaced large object's old segments are removed as the new object is written" : "a replaced large object's old segments are kept"}{/if}
            </p>
          </div>
        </section>

        <section aria-labelledby={`${id}-network`}>
          <h3 id={`${id}-network`}>Browsing and network</h3>
          {#each NETWORK_LIMITS as setting (setting.field)}
            {@render bounded(setting, current[setting.field])}
          {/each}
        </section>

        <section aria-labelledby={`${id}-records`}>
          <h3 id={`${id}-records`}>Records</h3>
          {#each RECORD_LIMITS as setting (setting.field)}
            {@render bounded(setting, current[setting.field])}
          {/each}
        </section>
      {/if}

      <section aria-labelledby={`${id}-backup`}>
        <h3 id={`${id}-backup`}>Backup and restore</h3>
        <div class="backup-actions">
          <button
            title="Back up settings and bookmarks to a file (passwords are not included)"
            onclick={() => settings.backUp()}
            disabled={settings.backingUp}
          >
            {settings.backingUp ? "Backing up…" : "Back up settings…"}
          </button>
          <button
            title="Restore settings and bookmarks from a backup file (passwords are re-entered on use)"
            onclick={() => settings.restore()}
            disabled={settings.restoring}
          >
            {settings.restoring ? "Restoring…" : "Restore settings…"}
          </button>
          <button
            title="Import bookmarks from an rclone.conf or a clouds.yaml (passwords go to the credential store)"
            onclick={onImportBookmarks}
          >
            Import bookmarks…
          </button>
        </div>
        {#if settings.backupMessage}
          <p class="note" role="status">{settings.backupMessage}</p>
        {/if}
        {#if settings.backupError}
          <p class="note error" role="alert">{settings.backupError}</p>
        {/if}
        {#if settings.restoreMessage}
          <p class="note" role="status">{settings.restoreMessage}</p>
        {/if}
        {#if settings.restoreError}
          <p class="note error" role="alert">{settings.restoreError}</p>
        {/if}
      </section>
    </div>

    <footer>
      <div class="budget-status" id={`${id}-budget-status`} role="status" aria-busy={settings.operation !== null}>
        {#if settings.operation === "get"}
          <span>Loading settings…</span>
        {:else if settings.status === "unavailable"}
          <span>Settings are unavailable.</span>
        {:else if settings.status === "stale" || settings.readError}
          <span>Settings could not be refreshed; the last observation is stale.</span>
        {/if}
        {#if settings.attempt?.state === "pending"}
          <span>Saving budget {settings.attempt.value}…</span>
        {:else if settings.attempt?.state === "unconfirmed"}
          <span>Budget change to {settings.attempt.value} is unconfirmed.</span>
        {:else if settings.attempt?.state === "refused"}
          <span>Budget change to {settings.attempt.value} was refused: {settings.attempt.reason}.</span>
        {:else if settings.attempt?.state === "acknowledged"}
          <span>Budget change acknowledged.</span>
        {/if}
        {#if settings.document}
          <span>Last observed budget: {settings.document.maxActiveJobs}.</span>
        {/if}
        {#if settings.status === "unconfirmed"}
          <span>Refresh settings before another change.</span>
        {:else if settings.status === "current" && settings.attempt?.state === "unconfirmed"}
          <span>Current settings were read; the earlier save was not acknowledged.</span>
        {/if}
      </div>
      <div class="footer-actions">
        <button disabled={settings.operation !== null} onclick={() => void settings.load()}>Refresh settings</button>
        <button onclick={onClose}>Close</button>
      </div>
    </footer>
  </div>
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: color-mix(in srgb, var(--color-bg) 60%, transparent);
    z-index: 30;
  }

  /* The body scrolls between a fixed heading and foot, so the dialog never
     runs past the window and its close and refresh stay in reach. */
  .dialog {
    width: min(40rem, calc(100vw - 4rem));
    max-height: calc(100vh - 4rem);
    display: flex;
    flex-direction: column;
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-raised);
  }

  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: var(--space-3) var(--space-4);
    border-bottom: 1px solid var(--color-border);
  }

  h2 {
    margin: 0;
    font-size: var(--text-base);
  }

  .close {
    font: inherit;
    background: none;
    border: none;
    color: var(--color-text-faint);
    cursor: pointer;
  }

  .body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 0 var(--space-4) var(--space-4);
  }

  section {
    padding-top: var(--space-4);
  }

  section + section {
    margin-top: var(--space-4);
    border-top: 1px solid var(--color-border);
  }

  h3 {
    margin: 0 0 var(--space-3);
    font-size: var(--text-sm);
    font-weight: var(--weight-semibold);
    color: var(--color-text-secondary);
  }

  .setting + .setting {
    margin-top: var(--space-4);
  }

  /* The label and its control share a line; the note and status sit under
     both at the full width of the text column. */
  .field {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2) var(--space-4);
    font-size: var(--text-sm);
    color: var(--color-text);
  }

  .field select,
  .field .number {
    font: inherit;
    font-size: var(--text-sm);
    background: var(--color-bg-card);
    color: var(--color-text);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    padding: 2px var(--space-2);
  }

  .field .number {
    width: 9em;
  }

  .field select:disabled,
  .field input:disabled {
    opacity: 0.6;
  }

  .note,
  .status {
    margin: var(--space-1) 0 0;
    font-size: var(--text-xs);
    color: var(--color-text-secondary);
    overflow-wrap: anywhere;
  }

  .status {
    color: var(--color-text);
  }

  /* A status with nothing to say takes no room; it stays in the page so a
     refusal written into it is announced. */
  .status:empty {
    margin: 0;
  }

  .error {
    color: var(--color-danger);
    font-weight: 600;
  }

  .backup-actions,
  .footer-actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
  }

  footer {
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    padding: var(--space-3) var(--space-4);
    border-top: 1px solid var(--color-border);
  }

  .budget-status {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-2);
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
  }

  .footer-actions {
    justify-content: flex-end;
  }

  .backup-actions button,
  .footer-actions button {
    padding: var(--space-1) var(--space-2);
    font: inherit;
    font-size: var(--text-sm);
    color: var(--color-text);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .backup-actions button:disabled,
  .footer-actions button:disabled {
    opacity: 0.6;
    cursor: default;
  }
</style>
