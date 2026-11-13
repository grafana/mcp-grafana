declare module 'virtual:shared-modules' {
  type Config = typeof import('../sharedModules.config');

  export const SHARED_MODULES_GLOBAL: Config['SHARED_MODULES_GLOBAL'];
  export const sharedModules: Readonly<Record<Config['SHARED_MODULE_IDS'][number], object>>;
}
