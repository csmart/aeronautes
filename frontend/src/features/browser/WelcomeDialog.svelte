<!-- First run (§54: teach without patronising). With no bookmark saved the
  shell shows three empty panes and a "+" badge; this names the one action
  that makes the application useful and what it asks for, then steps aside
  once a bookmark exists or the operator declines for this window. -->
<script lang="ts">
  import { trapFocus } from "../../lib/trapFocus";

  let { onAdd, onDismiss }: { onAdd: () => void; onDismiss: () => void } = $props();
</script>

<div class="scrim" role="presentation">
  <div class="welcome" role="dialog" aria-modal="true" aria-label="Welcome" tabindex="-1" use:trapFocus={onDismiss}>
    <h2 class="title">Welcome to Aeronautes</h2>
    <p>Aeronautes browses, inspects and moves data in OpenStack Swift accounts.</p>
    <p>
      Everything starts from a <strong>bookmark</strong>: one account's auth URL, user name and API
      key, kept together with how its TLS certificate is trusted. The key is handed to your operating
      system's credential store when one is available and is never written to the application's own
      database. You can verify the details against the endpoint before saving them.
    </p>
    <div class="actions">
      <button type="button" class="secondary" onclick={onDismiss}>Not now</button>
      <!-- svelte-ignore a11y_autofocus -->
      <button type="button" class="primary" onclick={onAdd} autofocus>Add a bookmark</button>
    </div>
  </div>
</div>

<style>
  .scrim {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: rgb(20 16 12 / 0.45);
    z-index: 10;
  }

  .welcome {
    width: min(30rem, calc(100vw - 2 * var(--space-5)));
    padding: var(--space-4);
    display: flex;
    flex-direction: column;
    gap: var(--space-3);
    background: var(--color-bg-pane);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-card);
    box-shadow: var(--shadow-raised);
  }

  .title {
    margin: 0;
    font-size: var(--text-lg);
    font-weight: var(--weight-semibold);
  }

  p {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-text-secondary);
    line-height: 1.5;
  }

  .actions {
    display: flex;
    justify-content: flex-end;
    gap: var(--space-2);
  }

  button {
    padding: var(--space-1) var(--space-3);
    font: inherit;
    font-size: var(--text-sm);
    border-radius: var(--radius-control);
    cursor: pointer;
  }

  .secondary {
    color: var(--color-text-secondary);
    background: var(--color-bg-card);
    border: 1px solid var(--color-border);
  }

  .primary {
    color: var(--color-text-invert);
    background: var(--color-accent);
    border: 1px solid var(--color-accent-strong);
  }
</style>
