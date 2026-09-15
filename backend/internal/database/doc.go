// Package database provides the shared SQL foundation. Constraints reject foreign
// ownership, duplicate sibling names within each table, invalid sizes and receipts
// that disagree with their expected hashes. Upload parts describe the immutable
// manifest; upload_part_copies records physical receipts separately.
//
// Consumers must still serialize changes to a directory by locking its folder row
// (or its user row for root) to enforce names across files and folders. They must
// also validate manifest ordering/totals, immutable content, upload transitions,
// lease generations and current membership within their transactions. Inserting
// a files row alone is not a publication algorithm. cluster_membership is the
// current set and changes together with the singleton configuration version.
//
// Receipt storage generations intentionally have no foreign key to the current
// node generation: historical receipts must survive volume replacement, while
// consumers exclude receipts for obsolete generations. A node's internal UUID
// remains separate from the configured textual NODE_ID.
package database
