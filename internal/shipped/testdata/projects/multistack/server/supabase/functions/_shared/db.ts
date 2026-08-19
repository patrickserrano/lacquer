/** A row in `public.items`. */
export interface ItemRow {
  id: string;
  title: string;
}

/** Returns every item belonging to `ownerId`. */
export function itemsQuery(ownerId: string): string {
  return `select id, title from public.items where owner_id = '${ownerId}'`;
}
