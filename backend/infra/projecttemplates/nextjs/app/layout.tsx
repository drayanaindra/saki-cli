import type { ReactNode } from 'react'

export const metadata = {
  title: 'New project',
  description: 'Scaffolded by Pipeline Studio',
}

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body style={{ fontFamily: 'system-ui, sans-serif' }}>{children}</body>
    </html>
  )
}
