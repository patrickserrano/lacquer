/** One row as the API returns it. */
export interface Item {
  id: string;
  title: string;
}

/** Fetches every item visible to the current session. */
export async function fetchItems(): Promise<Item[]> {
  const response = await fetch(`${process.env.NEXT_PUBLIC_API_URL}/items`);
  return (await response.json()) as Item[];
}
