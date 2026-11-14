// The third tool-input clip pass, after the two string passes in items.js. Sizes always use JSON.stringify's own UTF-8
// encoding, never the hub's Go encoding (U+2028/U+2029 differ). No whole input is encoded once per dropped item.

const inputClipMarker = 'zz_persona_clipped'
const jsonLen = (value) => Buffer.byteLength(JSON.stringify(value), 'utf8')

class InputClipNode {
  constructor(value, parent) {
    this.value = value
    this.parent = parent
    this.kind = ''
    this.key = ''
    this.keySize = 0
    this.kids = []
    this.fixed = 0
    this.size = 0
    this.order = 0
    this.active = true
    this.index = -1
  }

  remaining() { return this.kids.length - this.fixed }

  resize(delta) {
    for (let node = this; node !== null; node = node.parent) node.size += delta
  }

  decodedValue() {
    if (this.kind === '[') return this.kids.map((child) => child.decodedValue())
    if (this.kind === '{') return Object.fromEntries(this.kids.map((child) => [child.key, child.decodedValue()]))
    return this.value
  }
}

class InputClipTree {
  constructor(value) {
    this.arrays = []
    this.objects = []
    this.order = 0
    this.root = this.build(value, null)
  }

  build(value, parent) {
    const node = new InputClipNode(value, parent)
    if (Array.isArray(value)) {
      node.kind = '['
      node.order = this.order++
      this.arrays.push(node)
      node.size = 2 + Math.max(0, value.length - 1)
      for (const entry of value) {
        const child = this.build(entry, node)
        node.size += child.size
        node.kids.push(child)
      }
    } else if (value !== null && typeof value === 'object') {
      node.kind = '{'
      node.order = this.order++
      this.objects.push(node)
      const keys = Object.keys(value) // JSON.parse preserves the candidate's encoded key order
      node.size = 2 + Math.max(0, keys.length - 1)
      for (const key of keys) {
        const child = this.build(value[key], node)
        child.key = key
        child.keySize = jsonLen(key)
        node.size += child.keySize + 1 + child.size
        node.kids.push(child)
      }
    } else {
      node.size = jsonLen(value)
    }
    return node
  }
}

function heapLess(a, b) {
  return a.remaining() === b.remaining() ? a.order < b.order : a.remaining() > b.remaining()
}

function heapSwap(heap, i, j) {
  const node = heap[i]
  heap[i] = heap[j]
  heap[j] = node
  heap[i].index = i
  heap[j].index = j
}

function heapDown(heap, start) {
  let i = start
  for (;;) {
    const left = i * 2 + 1
    const right = left + 1
    let best = i
    if (left < heap.length && heapLess(heap[left], heap[best])) best = left
    if (right < heap.length && heapLess(heap[right], heap[best])) best = right
    if (best === i) return
    heapSwap(heap, i, best)
    i = best
  }
}

function heapFix(heap, index) {
  heapDown(heap, index) // removals and adding the fixed marker only lower a container's priority
}

function heapInit(heap) {
  heap.forEach((node, i) => { node.index = i })
  for (let i = Math.floor((heap.length - 2) / 2); i >= 0; i--) heapDown(heap, i)
}

function heapPop(heap) {
  const first = heap[0]
  const last = heap.pop()
  if (heap.length > 0) {
    heap[0] = last
    last.index = 0
    heapDown(heap, 0)
  }
  first.index = -1
  return first
}

function nextInputClipNode(heap) {
  while (heap.length > 0) {
    const node = heap[0]
    if (!node.active) {
      heapPop(heap)
      continue
    }
    return node.remaining() === 0 ? null : node
  }
  return null
}

function deactivateInputClipNode(node) {
  node.active = false
  for (const child of node.kids) deactivateInputClipNode(child)
}

function dropInputClipTail(heap, node) {
  const last = node.kids.length - 1 - node.fixed // fixed suffix children (the root marker) are never removed
  const child = node.kids[last]
  let saved = child.size
  if (node.kind === '{') saved += child.keySize + 1
  if (node.kids.length > 1) saved++
  node.kids.splice(last, 1)
  node.resize(-saved)
  deactivateInputClipNode(child)
  heapFix(heap, node.index)
}

function inputClipNote(originalSize, arrayItems, objectFields, clipNoteFormat) {
  return `Omitted ${arrayItems} array items and ${objectFields} object fields.${clipNoteFormat.replace('%d', String(originalSize))}`
}

/** Bound an over-budget object from the string passes, returning a new object whose JSON is at most maxInput bytes. */
export function boundInput(candidate, originalSize, maxInput, clipNoteFormat) {
  // Normalize once so sizes and surviving values have exactly the shapes JSON.stringify emits (including undefined in
  // arrays and special object keys). The source object and the two string-pass candidates are never mutated.
  const value = JSON.parse(JSON.stringify(candidate))
  const tree = new InputClipTree(value)
  const root = tree.root
  const arrays = tree.arrays
  const objects = tree.objects
  heapInit(arrays)
  heapInit(objects)

  let markerKey = inputClipMarker
  for (let suffix = 2; Object.hasOwn(value, markerKey); suffix++) markerKey = `${inputClipMarker}_${suffix}`
  const marker = new InputClipNode(inputClipNote(originalSize, 0, 0, clipNoteFormat), root)
  marker.key = markerKey
  marker.keySize = jsonLen(markerKey)
  marker.size = jsonLen(marker.value)
  let added = marker.keySize + 1 + marker.size
  if (root.kids.length > 0) added++
  root.kids.push(marker)
  root.fixed = 1
  root.resize(added)
  heapFix(objects, root.index)

  let arrayItems = 0
  let objectFields = 0
  while (root.size > maxInput) {
    const array = nextInputClipNode(arrays)
    if (array !== null) {
      dropInputClipTail(arrays, array)
      arrayItems++
    } else {
      const object = nextInputClipNode(objects)
      if (object === null) break // only the small root marker remains, which always fits
      dropInputClipTail(objects, object)
      objectFields++
    }
    const note = inputClipNote(originalSize, arrayItems, objectFields, clipNoteFormat)
    const delta = jsonLen(note) - marker.size
    marker.value = note
    marker.size += delta
    marker.parent.resize(delta)
  }
  return root.decodedValue()
}
