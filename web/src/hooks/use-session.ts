import { useQuery } from "@tanstack/react-query";
import { sessionApi } from "@/api/client-endpoints";

export function useSession() {
  return useQuery({
    queryKey: ["session"],
    queryFn: sessionApi.get,
    staleTime: Infinity,
    retry: false,
  });
}
