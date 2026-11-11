import type { TransferSettingsDTO } from "../../lib/api";

// What the operator typed into a transfer dialog's per-job settings (§38.3).
// An empty field leaves the value to the profile or the engine's default.
export interface JobSettingsInput {
  profile: string;
  chunkSize: string;
  transfers: string;
  bwLimit: string;
  bufferSize: string;
}

export function newJobSettings(profile: string): JobSettingsInput {
  return { profile, chunkSize: "", transfers: "", bwLimit: "", bufferSize: "" };
}

// The typed §44.1 overrides a request carries; zero and empty mean "not set",
// which the backend resolves against the profile.
export function requestSettings(input: JobSettingsInput): TransferSettingsDTO {
  return {
    chunkSize: input.chunkSize.trim(),
    transfers: Number.parseInt(input.transfers, 10) || 0,
    checkers: 0,
    tpsLimit: 0,
    bwLimit: input.bwLimit.trim(),
    bufferSize: input.bufferSize.trim(),
  };
}
