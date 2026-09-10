// Bun `with { type: "text" }` imports resolve to the file's string content.
declare module "*.css" {
  const text: string;
  export default text;
}
