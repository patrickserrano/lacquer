import { fetchItems } from "../lib/api";

/** The admin dashboard's index route. */
export default async function Page() {
  const items = await fetchItems();
  return (
    <ul>
      {items.map((item) => (
        <li key={item.id}>{item.title}</li>
      ))}
    </ul>
  );
}
