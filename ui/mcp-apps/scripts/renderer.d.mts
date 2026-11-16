// Types of scripts/renderer.mjs for the TypeScript files that import it (the Vitest setup and tests).
export interface Pin {
  repository: string;
  tag: string;
  signer: string;
  files: Record<string, string>;
}

export interface BundleVerifier {
  verify(bundle: unknown): unknown;
}

type Fetch = (url: string, init?: RequestInit) => Promise<Response>;

interface Options {
  dir?: string;
  fetchImpl?: Fetch;
  log?: (message: string) => void;
}

interface NetworkOptions extends Options {
  token?: string;
  verifier?: BundleVerifier;
}

export const rendererDir: string;
export const PIN_FILE: string;
export const REPOSITORY: string;
export const ISSUER: string;
export const SIGNER: string;
export const PROVENANCE: string;
export const REQUIRED_FILES: string[];

export function sha256(data: string | Uint8Array): string;
export function tagVersion(tag: string): string;
export function parsePin(text: string): Pin;
export function formatPin(pin: Pin): string;
export function downloadURL(repository: string, tag: string, name: string): string;
export function fetchRenderer(options?: Options): Promise<{ pin: Pin; downloaded: string[] }>;
export function verifyFile(
  name: string,
  data: Uint8Array,
  options: { repository: string; signer: string; verifier: BundleVerifier; fetchImpl?: Fetch; token?: string },
): Promise<void>;
export function verifyRenderer(options?: NetworkOptions): Promise<Pin>;
export function bumpRenderer(tag: string, options?: NetworkOptions): Promise<Pin>;
