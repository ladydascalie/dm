# hex

hex is the theme dm's documentation wears. The directory is self-contained: a
host links two stylesheets, drops the fonts beside them, and gets the theme.

| file | what it is |
| --- | --- |
| `tokens.css` | the token layer: colour, type, spacing, radii, shadows, motion |
| `fonts.css` | the `@font-face` rules for the two Atkinson families and the Shade subset |
| `fonts/` | the self-hosted variable fonts, latin, with their licences |
| `hex.css` | this site's map from the tokens onto Material, not part of the theme |

## Using it

Link `fonts.css` first, then `tokens.css`, and set the scheme on the root:

```html
<link rel="stylesheet" href="hex/fonts.css">
<link rel="stylesheet" href="hex/tokens.css">
```

```html
<html data-theme="dark">   <!-- :root is the dark tier, so this is the default -->
<html data-theme="light">  <!-- the light tier -->
```

Material writes its own scheme onto `<body>`, so the light tier answers
`body[data-md-color-scheme="default"]` as well as `data-theme="light"`. A host on
Material also links `hex.css`, which carries the mapping.

## The rules

- Use the tokens, never a raw value.
- Status is never the brand: `--success`, `--warn`, `--danger` and `--link` are
  distinct from `--accent`.
- Code surfaces stay dark in both schemes. Use the `--code-*` constants inside a
  code block; a theme role there renders dark on dark in the light tier.
- Radii are squared (`--radius-sm|md|lg`), spacing comes from `--space-*`, and
  interactive controls take their height from `--ctrl-pad-y`.
- Prose caps at `--measure`; tables and code keep the column.

## Licence

The token layer and `fonts.css` are FSL-1.1-MIT (`tokens.css.LICENSE`). The
fonts are SIL OFL 1.1, and each licence file travels beside the font it covers.
