import { control } from "./control.ts";

// Removes what the run created on every account it opened, and nothing else.
export default async function teardown() {
  const { failures } = await control.teardown();
  if (failures > 0) {
    throw new Error(`teardown could not remove ${failures} of the run's objects or containers`);
  }
}
