import { chakra } from "@chakra-ui/react";

type Props = {
  value: string; // empty string == "all"
  onChange: (next: string) => void;
  options: string[];
  allLabel?: string;
};

/**
 * A compact native select rendered to look at home in a chart title
 * row. The empty value means "no filter" (show the aggregated total).
 */
export function DeviceSelect({
  value,
  onChange,
  options,
  allLabel = "All",
}: Props) {
  return (
    <chakra.select
      value={value}
      onChange={(e) => onChange(e.currentTarget.value)}
      fontFamily="mono"
      fontSize="xs"
      bg="surface.subtle"
      color="fg.default"
      borderWidth="1px"
      borderColor="border.subtle"
      borderRadius="md"
      px="2"
      py="1"
      cursor="pointer"
      maxW="180px"
      _hover={{ borderColor: "border.default" }}
      _focusVisible={{
        outline: "none",
        borderColor: "fg.accent",
        boxShadow: "0 0 0 1px var(--chakra-colors-fg-accent)",
      }}
    >
      <option value="">{allLabel}</option>
      {options.map((opt) => (
        <option key={opt} value={opt}>
          {opt}
        </option>
      ))}
    </chakra.select>
  );
}
