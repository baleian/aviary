import { QueryClient } from "@tanstack/react-query";
import { ApiError } from "./api/client.ts";

// What the console has read from the API. A request that the API refused is
// not sent again by itself. One that got no answer is, twice.
export const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failures, error) => !(error instanceof ApiError) && failures < 2,
    },
  },
});
