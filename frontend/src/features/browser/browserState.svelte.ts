// Central UI state for the static browser (§65 commit 11): container
// modes (§52), prefix navigation over the fixture namespace, selection,
// and what the inspector looks at (§53). The live wiring swaps the
// fixture reads for BrowseService pages; navigation semantics stay.

import { accountFixture, containersFixture, fixtureObjects } from "../../lib/fixtures";
import { delimiterView, parentPrefix } from "../../lib/listing";
import type { ContainerEntry, InspectorTarget, ListingEntry } from "../../lib/types";

export type ContainerMode = "logical" | "all" | "backstage";

export interface ContainerGroups {
  /** Containers shown in the main list. */
  main: ContainerEntry[];
  /** Suspected segment stores, grouped separately in Logical mode (§52). */
  suspected: ContainerEntry[];
  /** Confirmed segment containers hidden by Logical mode. */
  hiddenConfirmed: number;
}

export class BrowserState {
  mode: ContainerMode = $state("logical");
  containerName: string | null = $state(null);
  prefix = $state("");
  /** Page-local filter (§5.1) — narrows loaded rows, never a server query. */
  filter = $state("");
  selectedIndex = $state(-1);

  readonly account = accountFixture;

  get container(): ContainerEntry | null {
    return containersFixture.find((c) => c.name === this.containerName) ?? null;
  }

  get containerGroups(): ContainerGroups {
    if (this.mode === "logical") {
      return {
        main: containersFixture.filter((c) => c.segmentRole === "normal"),
        suspected: containersFixture.filter((c) => c.segmentRole === "suspected"),
        hiddenConfirmed: containersFixture.filter((c) => c.segmentRole === "confirmed").length,
      };
    }
    return { main: containersFixture, suspected: [], hiddenConfirmed: 0 };
  }

  /** The working list: one delimiter page under the current prefix. */
  get entries(): ListingEntry[] {
    if (!this.containerName) {
      return [];
    }
    const view = delimiterView(fixtureObjects[this.containerName] ?? [], this.prefix);
    if (this.filter === "") {
      return view;
    }
    const needle = this.filter.toLowerCase();
    return view.filter((e) =>
      (e.kind === "object" ? e.name : e.prefix).toLowerCase().includes(needle),
    );
  }

  get selectedEntry(): ListingEntry | null {
    return this.entries[this.selectedIndex] ?? null;
  }

  /** The inspector follows selection; with none it shows the container,
   * and with no container open, the account (§53). */
  get inspectorTarget(): InspectorTarget {
    const container = this.container;
    if (!container) {
      return { kind: "account" };
    }
    const entry = this.selectedEntry;
    if (entry?.kind === "object") {
      return { kind: "object", container, object: entry };
    }
    if (entry?.kind === "prefix") {
      return { kind: "prefix", container, prefix: entry };
    }
    return { kind: "container", container };
  }

  openContainer(name: string) {
    this.containerName = name;
    this.prefix = "";
    this.filter = "";
    this.selectedIndex = -1;
  }

  /** Enter/double-click: prefixes drill in, objects just stay selected. */
  activate(entry: ListingEntry) {
    if (entry.kind === "prefix") {
      this.goTo(entry.prefix);
    }
  }

  /** Jump to a prefix within the current container (breadcrumb, drill). */
  goTo(prefix: string) {
    this.prefix = prefix;
    this.filter = "";
    this.selectedIndex = -1;
  }

  /** Backspace / Alt+↑ (§4.4): parent prefix, then out of the container. */
  up() {
    if (!this.containerName) {
      return;
    }
    if (this.prefix === "") {
      this.containerName = null;
    } else {
      this.prefix = parentPrefix(this.prefix);
    }
    this.filter = "";
    this.selectedIndex = -1;
  }
}
