// The packages this App shares with code loaded at run time, so that code uses the same React and Emotion instances
// (#1589). A second React breaks hooks, and a second Emotion cache splits the styles. sharedModules.config.ts lists
// them and generates virtual:shared-modules.
import { SHARED_MODULES_GLOBAL, sharedModules } from 'virtual:shared-modules';

export { SHARED_MODULES_GLOBAL, sharedModules };

/** Puts {@link sharedModules} on the global. Call it before the first render. */
export function exposeSharedModules(target: object = globalThis): void {
  Object.defineProperty(target, SHARED_MODULES_GLOBAL, { value: sharedModules, configurable: true });
}
