import { defineConfig } from 'vite'
import path from 'path'
import fs from 'fs'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'


function figmaAssetResolver() {
  return {
    name: 'figma-asset-resolver',
    resolveId(id) {
      if (id.startsWith('figma:asset/')) {
        const filename = id.replace('figma:asset/', '')
        return path.resolve(__dirname, 'src/assets', filename)
      }
    },
  }
}

function retainPreviousEntryAssets() {
  const outputDir = path.resolve(__dirname, '../internal/web/static')
  const indexPath = path.join(outputDir, 'index.html')
  const retained = new Map<string, Buffer>()
  const manifestPath = path.join(outputDir, '.vite', 'manifest.json')
  if (fs.existsSync(manifestPath)) {
    const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8')) as Record<string, {
      file?: string
      css?: string[]
      assets?: string[]
    }>
    for (const entry of Object.values(manifest)) {
      for (const relativePath of [entry.file, ...(entry.css || []), ...(entry.assets || [])]) {
        if (!relativePath) continue
        const assetPath = path.join(outputDir, relativePath)
        if (fs.existsSync(assetPath)) retained.set(relativePath, fs.readFileSync(assetPath))
      }
    }
  }
  if (fs.existsSync(indexPath)) {
    const html = fs.readFileSync(indexPath, 'utf8')
    for (const match of html.matchAll(/\/(assets\/index-[^"']+\.(?:js|css))/g)) {
      const relativePath = match[1]
      const assetPath = path.join(outputDir, relativePath)
      if (fs.existsSync(assetPath)) retained.set(relativePath, fs.readFileSync(assetPath))
    }
  }
  return {
    name: 'retain-previous-entry-assets',
    closeBundle() {
      for (const [relativePath, contents] of retained) {
        const assetPath = path.join(outputDir, relativePath)
        if (!fs.existsSync(assetPath)) {
          fs.mkdirSync(path.dirname(assetPath), { recursive: true })
          fs.writeFileSync(assetPath, contents)
        }
      }
    },
  }
}

export default defineConfig({
  base: '/',
  plugins: [
    figmaAssetResolver(),
    retainPreviousEntryAssets(),
    // The React and Tailwind plugins are both required for Make, even if
    // Tailwind is not being actively used – do not remove them
    react(),
    tailwindcss(),
  ],
  resolve: {
    alias: {
      // Alias @ to the src directory
      '@': path.resolve(__dirname, './src'),
    },
  },

  // File types to support raw imports. Never add .css, .tsx, or .ts files to this.
  assetsInclude: ['**/*.svg', '**/*.csv'],
  build: {
    outDir: '../internal/web/static',
    emptyOutDir: true,
    manifest: true,
  },
})
