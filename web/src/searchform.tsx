import { Button } from '@astryxdesign/core/Button';
import { HStack, StackItem, VStack } from '@astryxdesign/core/Layout';
import { Text } from '@astryxdesign/core/Text';
import { TextInput } from '@astryxdesign/core/TextInput';
import { useState } from 'react';
import { checkSearch, type TypedSearch } from './match';

interface Props {
  isSearching: boolean;
  onSearch: (search: TypedSearch) => void;
  onClear?: () => void;
}

export function SearchForm({ isSearching, onSearch, onClear }: Props) {
  const [text, setText] = useState('');
  const [year, setYear] = useState('');
  const [error, setError] = useState<{ error: string; field: 'q' | 'year' }>();
  const submit = () => {
    const input = checkSearch(text, year);
    if ('error' in input) {
      setError(input);
      return;
    }
    setError(undefined);
    onSearch(input);
  };
  const clear = () => {
    setText('');
    setYear('');
    setError(undefined);
    onClear?.();
  };
  const onKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'Enter') submit();
  };
  return (
    <VStack gap={2}>
      <HStack gap={2} vAlign="end" wrap="wrap">
        <StackItem size="fill">
          <TextInput
            label="Search"
            type="search"
            value={text}
            placeholder="Title, or an ID such as tmdb-48213"
            onChange={value => {
              setText(value);
              setError(undefined);
            }}
            onKeyDown={onKeyDown}
            status={error?.field === 'q' ? { type: 'error', message: error.error } : undefined}
          />
        </StackItem>
        <TextInput
          label="Year"
          value={year}
          width={96}
          inputMode="numeric"
          placeholder="Any"
          onChange={value => {
            setYear(value);
            setError(undefined);
          }}
          onKeyDown={onKeyDown}
          status={error?.field === 'year' ? { type: 'error', message: error.error } : undefined}
        />
        <Button label="Search" variant="secondary" isLoading={isSearching} onClick={submit} />
        {(onClear || error) && <Button label="Clear search" variant="ghost" onClick={clear} />}
      </HStack>
      <Text type="supporting" color="secondary">
        Search by title and an optional year, or by an ID such as tmdb-48213, tvdb-31207, or imdb-tt7712040. An ID search ignores the year.
      </Text>
    </VStack>
  );
}
