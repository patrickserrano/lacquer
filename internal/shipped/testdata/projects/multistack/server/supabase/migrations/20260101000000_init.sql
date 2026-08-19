create table public.items (
  id uuid primary key default gen_random_uuid(),
  owner_id uuid not null references auth.users (id) on delete cascade,
  title text not null
);

alter table public.items enable row level security;

create policy "owners read their own items"
  on public.items for select
  using (auth.uid() = owner_id);
