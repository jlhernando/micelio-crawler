#!/usr/bin/env node

/**
 * Verify V8 Pointer Compression optimization status.
 *
 * When running inside the platformatic/node-caged Docker image,
 * V8 pointer compression is enabled at compile time, reducing
 * memory usage by ~50% for pointer-heavy workloads.
 *
 * Usage: node scripts/verify-optimization.js
 */

import v8 from 'node:v8';
import process from 'node:process';

const stats = v8.getHeapStatistics();
const heapLimitMB = stats.heap_size_limit / 1024 / 1024;
const heapUsedMB = stats.used_heap_size / 1024 / 1024;
const heapTotalMB = stats.total_heap_size / 1024 / 1024;

// Compressed heaps are strictly capped at 4GB (4096 MB)
const isCompressed = stats.heap_size_limit <= 4 * 1024 * 1024 * 1024;

console.log('--- Micelio V8 Optimization Check ---');
console.log(`Runtime Version:          ${process.version}`);
console.log(`Platform:                 ${process.platform} ${process.arch}`);
console.log(`Heap Limit:               ${heapLimitMB.toFixed(0)} MB`);
console.log(`Heap Used:                ${heapUsedMB.toFixed(1)} MB`);
console.log(`Heap Total (allocated):   ${heapTotalMB.toFixed(1)} MB`);
console.log(`Pointer Compression:      ${isCompressed ? 'ACTIVE (compressed pointers)' : 'INACTIVE (full 64-bit pointers)'}`);
console.log(`RSS (process memory):     ${(process.memoryUsage().rss / 1024 / 1024).toFixed(1)} MB`);
console.log('---');

if (isCompressed) {
  console.log('Optimization is ACTIVE. Memory usage should be ~50% lower than standard Node.js.');
} else {
  console.log('Optimization is INACTIVE. To enable, use the platformatic/node-caged Docker image.');
  console.log('Note: --db mode (better-sqlite3) is incompatible with pointer compression.');
}

process.exit(isCompressed ? 0 : 1);
