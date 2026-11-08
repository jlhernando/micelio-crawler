const { test } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const { createRequire } = require('node:module');

// Resolve exactly as the build plugin does, including the npm override.
const pluginRequire = createRequire(path.resolve(__dirname, '../../node_modules/@sveltejs/vite-plugin-svelte/src/utils/options.js'));
const merge = pluginRequire('deepmerge');

for (const [name, makeTarget, makeSource, select] of [
 ['null-prototype target', () => Object.create(null), () => JSON.parse('{"__proto__":{"isAdmin":true}}'), x => x],
 ['own prototype key in target', () => JSON.parse('{"__proto__":{"existing":true}}'), () => JSON.parse('{"__proto__":{"isAdmin":true}}'), x => x],
 ['null nested placeholder', () => ({session:null}), () => JSON.parse('{"session":{"__proto__":{"isAdmin":true}}}'), x => x.session],
 ['undefined nested placeholder', () => ({session:undefined}), () => JSON.parse('{"session":{"__proto__":{"isAdmin":true}}}'), x => x.session],
 ['prototype key in target copy', () => JSON.parse('{"__proto__":{"isAdmin":true}}'), () => ({}), x => x],
 ['nested array item', () => ({}), () => ({items:[JSON.parse('{"__proto__":{"isAdmin":true}}')]}), x => x.items[0]],
]) {
 test('blocks returned-object poisoning: ' + name, () => {
  const result = select(merge(makeTarget(), makeSource()));
  assert.equal(Object.getPrototypeOf(result), Object.prototype);
  assert.equal(result.isAdmin, undefined);
  assert.equal({}.isAdmin, undefined);
 });
}

test('keeps normal merges, cloning, symbols and special objects', () => {
 const symbol = Symbol('option'); const date = new Date(123); const regex = /ok/i;
 const target = {nested:{left:1},list:[{a:1}],date,regex,[symbol]:{a:1}};
 const source = {nested:{right:2},list:[{b:2}],[symbol]:{b:2}};
 const result = merge(target,source);
 assert.deepEqual(result.nested,{left:1,right:2});
 assert.deepEqual(result.list,[{a:1},{b:2}]);
 assert.deepEqual(result[symbol],{a:1,b:2});
 assert.equal(result.date,date); assert.equal(result.regex,regex);
 assert.notEqual(result.nested,target.nested); assert.notEqual(result.list[0],target.list[0]);
 assert.deepEqual(target.nested,{left:1}); assert.deepEqual(source.nested,{right:2});
});

test('keeps the plugin array replacement and upstream custom merge options', () => {
 assert.deepEqual(merge({plugins:['old']},{plugins:['new']},{arrayMerge:(_a,b)=>b}),{plugins:['new']});
 assert.deepEqual(merge.all([{a:1},{b:2}]),{a:1,b:2});
 assert.deepEqual(merge({x:{a:1}},{x:{b:2}},{customMerge:key=>key==='x'?()=>({custom:true}):undefined}),{x:{custom:true}});
 const value={nested:true}; assert.equal(merge({}, {value},{clone:false}).value,value);
});

test('does not merge inherited constructor data up the prototype chain', () => {
 const result=merge({},JSON.parse('{"constructor":{"prototype":{"isAdmin":true}},"ok":1}'));
 assert.equal(Object.getPrototypeOf(result),Object.prototype);
 assert.equal(result.constructor,Object); assert.equal(result.ok,1); assert.equal({}.isAdmin,undefined);
});
