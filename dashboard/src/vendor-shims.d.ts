// Ambient shims for vendored graph libraries whose bundled .d.ts files
// reference type-only dependencies we don't install directly. These modules
// are only pulled in transitively by the sigma / esrap type
// declarations — the app never imports them itself, so `any`-typed stand-ins
// are sufficient to keep `svelte-check` clean without adding dev-dependencies.

// sigma's types.d.ts does `import { EventEmitter } from "events"` and uses it
// as a type (Node's built-in events module, no browser types installed).
declare module 'events' {
  export class EventEmitter {}
}

// esrap's index.d.ts imports the TSESTree namespace and reads TSESTree.Expression
// and TSESTree.Node off it as types.
declare module '@typescript-eslint/types' {
  export namespace TSESTree {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    type Expression = any;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    type Node = any;
  }
}
