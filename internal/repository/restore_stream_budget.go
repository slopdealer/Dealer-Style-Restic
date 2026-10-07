package repository

// Ordinary windows provide ciphertext headroom for the bounded worker. A
// single oversized blob keeps ordinary admission and may exceed this window.
// Request spans remain independent, and the frozen streamPack stays unchanged.
const restoreStreamWindow = 2 << 20
