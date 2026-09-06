<!-- Test-only wrapper: gives VirtualList a concrete row snippet and owns
  selection the way a real caller does. Not imported by application code. -->
<script lang="ts">
  import VirtualList from "./VirtualList.svelte";

  interface Props {
    items: string[];
    rowHeight?: number;
    viewport?: number;
    overscan?: number;
    selected?: number;
    onActivate?: (item: string, index: number) => void;
  }

  let { items, rowHeight = 36, viewport = 360, overscan = 2, selected = $bindable(-1), onActivate }: Props = $props();
</script>

<VirtualList
  {items}
  {rowHeight}
  {viewport}
  {overscan}
  {selected}
  onSelect={(i) => (selected = i)}
  {onActivate}
>
  {#snippet row(item, index, isSelected)}
    <span class="row-label" data-index={index} data-selected={isSelected}>{item}</span>
  {/snippet}
</VirtualList>
