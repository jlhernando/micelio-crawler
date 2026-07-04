// Ambient shims for vendored graph libraries whose bundled .d.ts files
// reference type-only dependencies we don't install directly. These modules
// are only pulled in transitively by the sigma / @cosmos.gl / esrap type
// declarations — the app never imports them itself, so `any`-typed stand-ins
// are sufficient to keep `svelte-check` clean without adding dev-dependencies.

// sigma's types.d.ts does `import { EventEmitter } from "events"` and uses it
// as a type (Node's built-in events module, no browser types installed).
declare module 'events' {
  export class EventEmitter {}
}

// @cosmos.gl/graph's config.d.ts imports these D3 event generics and uses them
// as types with 2–3 type arguments.
declare module 'd3-zoom' {
  export type D3ZoomEvent<A = unknown, B = unknown> = unknown;
}
declare module 'd3-drag' {
  export type D3DragEvent<A = unknown, B = unknown, C = unknown> = unknown;
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

// @cosmos.gl/graph's helper.d.ts references the DOMPurify namespace for its
// sanitize options without bundling the type. Provide a minimal stand-in.
declare namespace DOMPurify {
  type Config = Record<string, unknown>;
}
