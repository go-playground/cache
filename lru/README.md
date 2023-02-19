# LRU

This is a Least Recently Used cache backed by a generic doubly linked list with O(1) time complexity.

# When to use
You would typically use an LRU cache when:

- Capacity of cache will hold nearly all data.
- Entries being used are being used on a consistent frequency.

Both above will prevent large amounts of data flapping in and out of the cache.
If your cache can only hold a fraction of values being stored or data seem on a cadence but cache hit counts for that is higher than most others in the cached data, check out using the LFU cache instead. 